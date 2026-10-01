package main

import (
	"fmt"
	"net/http"
	"sync"
)

const (
	authorizedMaintainer  = "maintainer-lu"
	authorizedMaintainer2 = "maintainer-zheng"
	unauthorizedOperator  = "operator-chen"
	clearanceReference    = "WO-20260930-17"
)

func runClearGateFault(client *apiClient) error {
	if _, err := fixtureGate(client, "gate-clear", "故障演练闸", 4, 0.5); err != nil {
		return err
	}
	_, abortedCommand, err := fixtureFaultedGate(client, "gate-clear", 3.0)
	if err != nil {
		return err
	}
	abortedID, err := stringField(abortedCommand, "id")
	if err != nil {
		return err
	}
	abortedStatus, err := stringField(abortedCommand, "status")
	if err != nil {
		return err
	}
	if abortedStatus != "aborted" {
		return fmt.Errorf("fault execution: expected command aborted, got %q", abortedStatus)
	}

	// Read the latched gate: it must advertise clearable conditions.
	faultedGate, err := readGate(client, "gate-clear")
	if err != nil {
		return err
	}
	if err := expectFaultedGate(faultedGate, abortedID, 3.0); err != nil {
		return err
	}
	eligibility, err := objectField(faultedGate, "clearance_eligibility")
	if err != nil {
		return err
	}
	if eligible, _ := boolField(eligibility, "eligible"); !eligible {
		return fmt.Errorf("faulted gate should be clearable, got %s", prettyBody(eligibility))
	}
	if established, _ := boolField(eligibility, "fault_established"); !established {
		return fmt.Errorf("fault fact should be established, got %s", prettyBody(eligibility))
	}
	if occupied, _ := boolField(eligibility, "has_occupancy"); occupied {
		return fmt.Errorf("faulted gate should report no occupancy, got %s", prettyBody(eligibility))
	}

	// While latched, new commands must be rejected so the gate cannot rejoin
	// scheduling without going through the formal clearance path.
	blockedCommand, err := client.post("/commands", map[string]any{
		"gate_id":         "gate-clear",
		"target_aperture": 1.0,
		"reason":          "锁存期间尝试调度",
		"requested_by":    "dispatcher-lin",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(blockedCommand, http.StatusConflict, "reject command on faulted gate"); err != nil {
		return err
	}
	if code, _ := errorCode(blockedCommand); code != "gate_faulted" {
		return fmt.Errorf("faulted gate command error code: expected gate_faulted, got %q", code)
	}

	// The command read must agree with the gate: aborted, token gone.
	commandResult, err := client.get("/commands/" + abortedID)
	if err != nil {
		return err
	}
	if err := requireStatus(commandResult, http.StatusOK, "read aborted command"); err != nil {
		return err
	}
	readCommand, err := objectField(commandResult.Body, "command")
	if err != nil {
		return err
	}
	if status, _ := stringField(readCommand, "status"); status != "aborted" {
		return fmt.Errorf("stored command status: expected aborted, got %q", status)
	}
	if gateID, _ := stringField(readCommand, "gate_id"); gateID != "gate-clear" {
		return fmt.Errorf("aborted command references unexpected gate %q", gateID)
	}
	if terminalReason, _ := stringField(readCommand, "terminal_reason"); terminalReason == "" {
		return fmt.Errorf("aborted command must retain its terminal reason")
	}
	if token, exists := readCommand["execution_token"]; exists && token != "" {
		return fmt.Errorf("aborted command must not retain an execution token, got %v", token)
	}

	beforeVersion, err := intField(faultedGate, "version")
	if err != nil {
		return err
	}

	// Insufficient evidence: missing reference must fail without changing state.
	missingReference, err := client.post("/gates/gate-clear/clearance", map[string]any{
		"handled_by": authorizedMaintainer,
		"reference":  "  ",
		"note":       "现场已处置",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(missingReference, http.StatusBadRequest, "reject clearance without reference"); err != nil {
		return err
	}
	if code, _ := errorCode(missingReference); code != "invalid_clearance_reference" {
		return fmt.Errorf("missing reference error code: expected invalid_clearance_reference, got %q", code)
	}

	// Insufficient evidence: missing handler identity.
	missingHandler, err := client.post("/gates/gate-clear/clearance", map[string]any{
		"reference": clearanceReference,
		"note":      "现场已处置",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(missingHandler, http.StatusBadRequest, "reject clearance without handler"); err != nil {
		return err
	}
	if code, _ := errorCode(missingHandler); code != "invalid_handled_by" {
		return fmt.Errorf("missing handler error code: expected invalid_handled_by, got %q", code)
	}

	// Bypass attempt by an operator outside the maintainer allow list.
	forbidden, err := client.post("/gates/gate-clear/clearance", map[string]any{
		"handled_by": unauthorizedOperator,
		"reference":  clearanceReference,
		"note":       "现场已处置但身份无权限",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(forbidden, http.StatusForbidden, "reject unauthorized maintainer"); err != nil {
		return err
	}
	if code, _ := errorCode(forbidden); code != "maintainer_not_authorized" {
		return fmt.Errorf("unauthorized error code: expected maintainer_not_authorized, got %q", code)
	}

	if err := expectGateVersion(client, "gate-clear", beforeVersion, "failed clearances must not change the gate"); err != nil {
		return err
	}

	// Concurrent clearances: exactly one must win, the other must fail
	// against the already-cleared gate without any state corruption.
	var wg sync.WaitGroup
	results := make([]apiResult, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], _ = client.post("/gates/gate-clear/clearance", map[string]any{
			"handled_by": authorizedMaintainer,
			"reference":  clearanceReference,
			"note":       "现场更换开度传感器并复核限位",
		})
	}()
	go func() {
		defer wg.Done()
		results[1], _ = client.post("/gates/gate-clear/clearance", map[string]any{
			"handled_by": authorizedMaintainer2,
			"reference":  clearanceReference + "-dup",
			"note":       "并发清除验证",
		})
	}()
	wg.Wait()

	okCount, conflictCount := 0, 0
	var winner apiResult
	for index, result := range results {
		switch result.Status {
		case http.StatusOK:
			okCount++
			winner = result
		case http.StatusConflict:
			conflictCount++
			if code, _ := errorCode(result); code != "gate_not_faulted" {
				return fmt.Errorf("concurrent loser %d error code: expected gate_not_faulted, got %q", index, code)
			}
		default:
			return fmt.Errorf("concurrent clearance %d returned unexpected HTTP %d", index, result.Status)
		}
	}
	if okCount != 1 || conflictCount != 1 {
		return fmt.Errorf("concurrent clearance: expected exactly one success and one conflict, got %d success / %d conflict", okCount, conflictCount)
	}

	clearedGate, err := objectField(winner.Body, "gate")
	if err != nil {
		return err
	}
	if status, _ := stringField(clearedGate, "status"); status != "idle" {
		return fmt.Errorf("cleared gate status: expected idle, got %q", status)
	}
	if aperture, _ := floatField(clearedGate, "aperture"); aperture != 3.0 {
		return fmt.Errorf("cleared gate aperture must be retained at 3.0, got %v", aperture)
	}
	if _, exists := clearedGate["active_command_id"]; exists {
		return fmt.Errorf("cleared gate must not reference an active command")
	}
	clearedVersion, err := intField(clearedGate, "version")
	if err != nil {
		return err
	}
	if clearedVersion != beforeVersion+1 {
		return fmt.Errorf("clearance version: expected %d, got %d", beforeVersion+1, clearedVersion)
	}
	clearance, err := objectField(winner.Body, "clearance")
	if err != nil {
		return err
	}
	if handler, _ := stringField(clearance, "handled_by"); handler != authorizedMaintainer && handler != authorizedMaintainer2 {
		return fmt.Errorf("clearance response names unexpected handler %q", handler)
	}
	if reference, _ := stringField(clearance, "reference"); reference == "" {
		return fmt.Errorf("clearance response must echo the handling reference")
	}
	clearedAt, err := stringField(clearance, "cleared_at")
	if err != nil {
		return err
	}
	if gateUpdatedAt, _ := stringField(clearedGate, "updated_at"); gateUpdatedAt != clearedAt {
		return fmt.Errorf("clearance timestamp mismatch: clearance=%q gate.updated_at=%q", clearedAt, gateUpdatedAt)
	}

	// Fresh reads must agree with the command result and explain why the
	// gate is no longer clearable.
	idleGate, err := readGate(client, "gate-clear")
	if err != nil {
		return err
	}
	if status, _ := stringField(idleGate, "status"); status != "idle" {
		return fmt.Errorf("read gate status: expected idle, got %q", status)
	}
	fault, err := objectField(idleGate, "fault")
	if err != nil {
		return fmt.Errorf("cleared gate should retain the historical fault record: %w", err)
	}
	if faultCommandID, _ := stringField(fault, "command_id"); faultCommandID != abortedID {
		return fmt.Errorf("retained fault record: expected command %s, got %q", abortedID, faultCommandID)
	}
	lastClearance, err := objectField(idleGate, "last_clearance")
	if err != nil {
		return fmt.Errorf("read gate must expose the recorded clearance: %w", err)
	}
	if reference, _ := stringField(lastClearance, "reference"); reference == "" {
		return fmt.Errorf("recorded clearance must retain the handling reference")
	}
	if storedAt, _ := stringField(lastClearance, "cleared_at"); storedAt != clearedAt {
		return fmt.Errorf("recorded clearance timestamp: expected %q, got %q", clearedAt, storedAt)
	}
	idleEligibility, err := objectField(idleGate, "clearance_eligibility")
	if err != nil {
		return err
	}
	if eligible, _ := boolField(idleEligibility, "eligible"); eligible {
		return fmt.Errorf("idle gate must not be reported as clearable")
	}
	if established, _ := boolField(idleEligibility, "fault_established"); established {
		return fmt.Errorf("idle gate must not report an active fault fact")
	}
	reasons, err := stringSliceField(idleEligibility, "reasons")
	if err != nil {
		return err
	}
	if len(reasons) == 0 {
		return fmt.Errorf("idle gate clearance report must explain why it is not clearable")
	}

	// Repeated clearance is a stable conflict and leaves state untouched.
	repeat, err := client.post("/gates/gate-clear/clearance", map[string]any{
		"handled_by": authorizedMaintainer,
		"reference":  clearanceReference,
		"note":       "重复清除",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(repeat, http.StatusConflict, "reject repeated clearance"); err != nil {
		return err
	}
	if code, _ := errorCode(repeat); code != "gate_not_faulted" {
		return fmt.Errorf("repeat clearance error code: expected gate_not_faulted, got %q", code)
	}
	if err := expectGateVersion(client, "gate-clear", clearedVersion, "repeated clearance must not change the gate"); err != nil {
		return err
	}

	// New commands after recovery must read the retained aperture.
	recoveredCommand, err := fixtureCommand(client, "gate-clear", 1.0, "恢复后重新调度", "dispatcher-lin")
	if err != nil {
		return err
	}
	if starting, _ := floatField(recoveredCommand, "starting_aperture"); starting != 3.0 {
		return fmt.Errorf("recovered command starting aperture: expected retained 3.0, got %v", starting)
	}
	recoveredID, err := stringField(recoveredCommand, "id")
	if err != nil {
		return err
	}
	approved, err := fixtureClearReview(client, recoveredID, "safety-he")
	if err != nil {
		return err
	}
	token, err := stringField(approved, "execution_token")
	if err != nil {
		return err
	}
	reservedGate, err := readGate(client, "gate-clear")
	if err != nil {
		return err
	}
	if status, _ := stringField(reservedGate, "status"); status != "reserved" {
		return fmt.Errorf("recovered gate reservation: expected reserved, got %q", status)
	}
	if active, _ := stringField(reservedGate, "active_command_id"); active != recoveredID {
		return fmt.Errorf("recovered gate reservation: expected active command %s, got %q", recoveredID, active)
	}
	executed, err := client.post("/commands/"+recoveredID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          token,
		"simulate_fault": false,
	})
	if err != nil {
		return err
	}
	if err := requireStatus(executed, http.StatusOK, "execute recovered command"); err != nil {
		return err
	}
	finalGate, err := objectField(executed.Body, "gate")
	if err != nil {
		return err
	}
	if status, _ := stringField(finalGate, "status"); status != "idle" {
		return fmt.Errorf("recovered execution gate status: expected idle, got %q", status)
	}
	if aperture, _ := floatField(finalGate, "aperture"); aperture != 1.0 {
		return fmt.Errorf("recovered execution aperture: expected 1.0, got %v", aperture)
	}

	// Direct clearance against a gate that is not faulted is a bypass and fails.
	bypass, err := client.post("/gates/gate-clear/clearance", map[string]any{
		"handled_by": authorizedMaintainer,
		"reference":  "WO-bypass",
	})
	if err != nil {
		return err
	}
	if err := requireStatus(bypass, http.StatusConflict, "reject clearance bypass on idle gate"); err != nil {
		return err
	}
	if code, _ := errorCode(bypass); code != "gate_not_faulted" {
		return fmt.Errorf("bypass error code: expected gate_not_faulted, got %q", code)
	}
	missing, err := client.post("/gates/unknown-gate/clearance", map[string]any{
		"handled_by": authorizedMaintainer,
		"reference":  clearanceReference,
	})
	if err != nil {
		return err
	}
	if err := requireStatus(missing, http.StatusNotFound, "reject clearance for unknown gate"); err != nil {
		return err
	}

	fmt.Println("clear-gate-fault: faulted gate cleared once with evidence; evidence, auth, duplicate, concurrent and bypass attempts handled")
	return nil
}

func readGate(client *apiClient, id string) (map[string]any, error) {
	result, err := client.get("/gates/" + id)
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusOK, "read gate"); err != nil {
		return nil, err
	}
	return objectField(result.Body, "gate")
}

func expectFaultedGate(gate map[string]any, expectedCommandID string, expectedAperture float64) error {
	status, err := stringField(gate, "status")
	if err != nil {
		return err
	}
	if status != "faulted" {
		return fmt.Errorf("faulted gate status: expected faulted, got %q", status)
	}
	aperture, err := floatField(gate, "aperture")
	if err != nil {
		return err
	}
	if aperture != expectedAperture {
		return fmt.Errorf("faulted gate aperture: expected retained %v, got %v", expectedAperture, aperture)
	}
	if _, exists := gate["active_command_id"]; exists {
		return fmt.Errorf("faulted gate must not retain an active command reference")
	}
	fault, err := objectField(gate, "fault")
	if err != nil {
		return fmt.Errorf("faulted gate must record the fault fact: %w", err)
	}
	if commandID, _ := stringField(fault, "command_id"); commandID != expectedCommandID {
		return fmt.Errorf("fault record command: expected %s, got %q", expectedCommandID, commandID)
	}
	if reason, _ := stringField(fault, "reason"); reason == "" {
		return fmt.Errorf("fault record must retain the fault reason")
	}
	if faultAperture, _ := floatField(fault, "aperture"); faultAperture != expectedAperture {
		return fmt.Errorf("fault record aperture: expected %v, got %v", expectedAperture, faultAperture)
	}
	return nil
}

func expectGateVersion(client *apiClient, id string, expected int, operation string) error {
	gate, err := readGate(client, id)
	if err != nil {
		return err
	}
	version, err := intField(gate, "version")
	if err != nil {
		return err
	}
	if version != expected {
		return fmt.Errorf("%s: expected version %d, got %d", operation, expected, version)
	}
	return nil
}

func stringSliceField(object map[string]any, name string) ([]string, error) {
	value, exists := object[name]
	if !exists {
		return nil, fmt.Errorf("object is missing array field %q", name)
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("object field %q is not an array", name)
	}
	items := make([]string, 0, len(raw))
	for _, element := range raw {
		text, ok := element.(string)
		if !ok {
			return nil, fmt.Errorf("array field %q contains a non-string element", name)
		}
		items = append(items, text)
	}
	return items, nil
}
