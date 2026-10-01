package main

import (
	"fmt"
	"net/http"
	"sync"
)

const validEvidence = "现场人工盘车复核，液压与机械限位恢复正常，已重新挂接安全销"

func postClearance(client *apiClient, gateID, commandID, handler, evidence string) (apiResult, error) {
	return client.post("/gates/"+gateID+"/clearance", map[string]any{
		"command_id": commandID,
		"handler":    handler,
		"evidence":   evidence,
		"note":       "处置完成申请恢复",
	})
}

func runClearGateFault(client *apiClient) error {
	// --- Setup: reserve the gate with command A, then leave a pending command
	// B in flight before executing A with a simulated fault. B represents an
	// unfinished occupation that must block clearance. ---
	if _, err := fixtureGate(client, "gate-clear", "待清除泄洪闸", 5, 0.25); err != nil {
		return err
	}
	commandA, err := fixtureCommand(client, "gate-clear", 3.0, "执行故障流程验证", "dispatcher-lin")
	if err != nil {
		return err
	}
	commandID, err := stringField(commandA, "id")
	if err != nil {
		return err
	}
	approved, err := fixtureClearReview(client, commandID, "safety-wu")
	if err != nil {
		return err
	}
	token, err := stringField(approved, "execution_token")
	if err != nil {
		return err
	}
	leftoverB, err := fixtureCommand(client, "gate-clear", 1.25, "锁存前残留的待复核命令", "dispatcher-lin")
	if err != nil {
		return err
	}
	leftoverID, err := stringField(leftoverB, "id")
	if err != nil {
		return err
	}

	faulted, err := client.post("/commands/"+commandID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          token,
		"simulate_fault": true,
	})
	if err != nil {
		return err
	}
	if err := requireStatus(faulted, http.StatusOK, "simulate execution fault"); err != nil {
		return err
	}
	faultedGate, err := objectField(faulted.Body, "gate")
	if err != nil {
		return err
	}
	gateStatus, err := stringField(faultedGate, "status")
	if err != nil {
		return err
	}
	if gateStatus != "faulted" {
		return fmt.Errorf("latched gate status: expected faulted, got %q", gateStatus)
	}
	faultObject, err := objectField(faultedGate, "fault")
	if err != nil {
		return err
	}
	if faultCommand, err := stringField(faultObject, "command_id"); err != nil || faultCommand != commandID {
		return fmt.Errorf("fault record command: expected %q, got %q (err=%v)", commandID, faultCommand, err)
	}
	abortedCommand, err := objectField(faulted.Body, "command")
	if err != nil {
		return err
	}
	if status, err := stringField(abortedCommand, "status"); err != nil || status != "aborted" {
		return fmt.Errorf("faulting command status: expected aborted, got %q (err=%v)", status, err)
	}

	// Bypass: a faulted gate must reject new commands until formally cleared.
	bypass, err := client.post("/commands", map[string]any{
		"gate_id":         "gate-clear",
		"target_aperture": 1.0,
		"reason":          "绕过清除直接下命令",
		"requested_by":    "dispatcher-lin",
	})
	if err != nil {
		return err
	}
	if err := requireErrorCode(bypass, http.StatusConflict, "gate_faulted", "reject command bypassing clearance"); err != nil {
		return err
	}

	// Insufficient evidence and missing handler are stable 400s.
	thin, err := postClearance(client, "gate-clear", commandID, "ops-qin", "无")
	if err != nil {
		return err
	}
	if err := requireErrorCode(thin, http.StatusBadRequest, "insufficient_clearance_evidence", "reject thin evidence"); err != nil {
		return err
	}
	noHandler, err := client.post("/gates/gate-clear/clearance", map[string]any{
		"command_id": commandID,
		"evidence":   validEvidence,
	})
	if err != nil {
		return err
	}
	if err := requireErrorCode(noHandler, http.StatusBadRequest, "invalid_handler", "reject missing handler"); err != nil {
		return err
	}

	// Authorization: dispatcher/operator/verifier identities cannot clear.
	unauthorized, err := postClearance(client, "gate-clear", commandID, "dispatcher-lin", validEvidence)
	if err != nil {
		return err
	}
	if err := requireErrorCode(unauthorized, http.StatusForbidden, "clearance_not_authorized", "reject unauthorized handler"); err != nil {
		return err
	}

	// Unknown gate and unknown faulting command are stable 404s.
	missingGate, err := postClearance(client, "gate-missing", commandID, "ops-qin", validEvidence)
	if err != nil {
		return err
	}
	if err := requireErrorCode(missingGate, http.StatusNotFound, "gate_not_found", "reject clearance on missing gate"); err != nil {
		return err
	}
	missingCommand, err := postClearance(client, "gate-clear", "cmd-does-not-exist", "ops-qin", validEvidence)
	if err != nil {
		return err
	}
	if err := requireErrorCode(missingCommand, http.StatusNotFound, "command_not_found", "reject clearance referencing missing command"); err != nil {
		return err
	}

	// Evidence must reference the explicit fault fact: another gate's aborted
	// command cannot clear this gate.
	otherID, _, _, err := fixtureFaultedGate(client, "gate-other", 2.0)
	if err != nil {
		return err
	}
	wrongFault, err := postClearance(client, "gate-clear", otherID, "ops-qin", validEvidence)
	if err != nil {
		return err
	}
	if err := requireErrorCode(wrongFault, http.StatusConflict, "fault_command_mismatch", "reject clearance referencing another fault"); err != nil {
		return err
	}

	// The leftover pending command B blocks clearance even though
	// ActiveCommandID is already empty after the latch.
	occupied, err := postClearance(client, "gate-clear", commandID, "ops-qin", validEvidence)
	if err != nil {
		return err
	}
	if err := requireErrorCode(occupied, http.StatusConflict, "gate_has_open_command", "reject clearance while a command is open"); err != nil {
		return err
	}

	// Every failed attempt must leave the latched state untouched.
	unchanged, err := client.get("/gates/gate-clear")
	if err != nil {
		return err
	}
	if err := requireStatus(unchanged, http.StatusOK, "read still-faulted gate"); err != nil {
		return err
	}
	unchangedGate, err := objectField(unchanged.Body, "gate")
	if err != nil {
		return err
	}
	if status, err := stringField(unchangedGate, "status"); err != nil || status != "faulted" {
		return fmt.Errorf("gate changed after failed clearances: status=%q (err=%v)", status, err)
	}
	aperture, err := floatField(unchangedGate, "aperture")
	if err != nil {
		return err
	}
	if aperture != 3.0 {
		return fmt.Errorf("faulted gate aperture drifted: expected 3.0, got %v", aperture)
	}

	// Terminate the leftover command so no open occupation remains. A blocked
	// interlock review rejects B without reserving the (faulted) gate.
	blocked, err := client.post("/commands/"+leftoverID+"/interlock", map[string]any{
		"verdict":  "blocked",
		"verifier": "safety-wu",
		"note":     "闸门锁存，拒绝残留命令",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(blocked, http.StatusOK, "block leftover pending command"); err != nil {
		return err
	}

	// Concurrent clears: exactly one must win, the rest see the gate already idle.
	const contenders = 8
	var wg sync.WaitGroup
	results := make([]apiResult, contenders)
	callErrs := make([]error, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], callErrs[i] = postClearance(client, "gate-clear", commandID, "ops-qin", validEvidence)
		}(i)
	}
	wg.Wait()
	successes, conflicts := 0, 0
	for i, callErr := range callErrs {
		if callErr != nil {
			return callErr
		}
		switch results[i].Status {
		case http.StatusOK:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("concurrent clearance %d returned unexpected status %d: %s", i, results[i].Status, prettyBody(results[i].Body))
		}
	}
	if successes != 1 || conflicts != contenders-1 {
		return fmt.Errorf("concurrent clearance winners: expected exactly 1 success, got %d success / %d conflict", successes, conflicts)
	}

	// The winning response must make gate, clearance and aborted command
	// mutually explain one another.
	winner := winningResult(results)
	clearedGate, err := objectField(winner.Body, "gate")
	if err != nil {
		return err
	}
	if status, err := stringField(clearedGate, "status"); err != nil || status != "idle" {
		return fmt.Errorf("cleared gate status: expected idle, got %q (err=%v)", status, err)
	}
	if active, err := stringField(clearedGate, "active_command_id"); err == nil && active != "" {
		return fmt.Errorf("cleared gate still references active command %q", active)
	}
	clearanceObject, err := objectField(winner.Body, "clearance")
	if err != nil {
		return err
	}
	if ref, err := stringField(clearanceObject, "fault_command_id"); err != nil || ref != commandID {
		return fmt.Errorf("clearance fault_command_id: expected %q, got %q (err=%v)", commandID, ref, err)
	}
	if handler, err := stringField(clearanceObject, "handler"); err != nil || handler != "ops-qin" {
		return fmt.Errorf("clearance handler mismatch: %q (err=%v)", handler, err)
	}
	if evidence, err := stringField(clearanceObject, "evidence"); err != nil || evidence != validEvidence {
		return fmt.Errorf("clearance evidence mismatch (err=%v)", err)
	}
	clearedCommandObject, err := objectField(winner.Body, "command")
	if err != nil {
		return err
	}
	if status, err := stringField(clearedCommandObject, "status"); err != nil || status != "aborted" {
		return fmt.Errorf("faulting command status in clearance response: expected aborted, got %q (err=%v)", status, err)
	}

	// Duplicate clearance after success is a stable conflict and changes nothing.
	duplicate, err := postClearance(client, "gate-clear", commandID, "ops-qin", validEvidence)
	if err != nil {
		return err
	}
	if err := requireErrorCode(duplicate, http.StatusConflict, "gate_not_faulted", "reject duplicate clearance"); err != nil {
		return err
	}

	// Clearing against a never-faulted idle gate is a stable conflict.
	if _, err := fixtureGate(client, "gate-idle", "始终空闲闸", 4, 0.5); err != nil {
		return err
	}
	idleFaultCmd, _, _, err := fixtureFaultedGate(client, "gate-other2", 2.5)
	if err != nil {
		return err
	}
	idleClear, err := postClearance(client, "gate-idle", idleFaultCmd, "ops-qin", validEvidence)
	if err != nil {
		return err
	}
	if err := requireErrorCode(idleClear, http.StatusConflict, "fault_command_mismatch", "reject clearance against an idle gate"); err != nil {
		return err
	}

	// After clearance the gate re-enters scheduling: a fresh command reads the
	// retained aperture as its frozen starting point and flows through review.
	fresh, err := fixtureCommand(client, "gate-clear", 0.75, "解除后恢复调度", "dispatcher-lin")
	if err != nil {
		return err
	}
	if starting, err := floatField(fresh, "starting_aperture"); err != nil || starting != 3.0 {
		return fmt.Errorf("post-clearance starting aperture: expected 3.0, got %v (err=%v)", starting, err)
	}
	freshID, err := stringField(fresh, "id")
	if err != nil {
		return err
	}
	reapproved, err := fixtureClearReview(client, freshID, "safety-wu")
	if err != nil {
		return err
	}
	if status, err := stringField(reapproved, "status"); err != nil || status != "approved" {
		return fmt.Errorf("post-clearance review status: expected approved, got %q (err=%v)", status, err)
	}

	// Final read: gate is reserved by the new command and still carries the
	// clearance audit record referencing the original aborted command.
	finalRead, err := client.get("/gates/gate-clear")
	if err != nil {
		return err
	}
	finalGate, err := objectField(finalRead.Body, "gate")
	if err != nil {
		return err
	}
	if status, err := stringField(finalGate, "status"); err != nil || status != "reserved" {
		return fmt.Errorf("final gate status: expected reserved, got %q (err=%v)", status, err)
	}
	if active, err := stringField(finalGate, "active_command_id"); err != nil || active != freshID {
		return fmt.Errorf("final gate active command: expected %q, got %q (err=%v)", freshID, active, err)
	}
	finalClearance, err := objectField(finalGate, "last_clearance")
	if err != nil {
		return err
	}
	if ref, err := stringField(finalClearance, "fault_command_id"); err != nil || ref != commandID {
		return fmt.Errorf("final clearance audit fault_command_id: expected %q, got %q (err=%v)", commandID, ref, err)
	}

	fmt.Println("clear-gate-fault: guarded clearance restored idle; weak/duplicate/concurrent/bypass attempts rejected without state change")
	return nil
}

func winningResult(results []apiResult) apiResult {
	for _, r := range results {
		if r.Status == http.StatusOK {
			return r
		}
	}
	return apiResult{}
}
