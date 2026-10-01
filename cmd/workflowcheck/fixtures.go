package main

import (
	"fmt"
	"net/http"
)

func fixtureGate(
	client *apiClient,
	id string,
	name string,
	maxAperture float64,
	initialAperture float64,
) (map[string]any, error) {
	result, err := client.post("/gates", map[string]any{
		"id":               id,
		"name":             name,
		"max_aperture":     maxAperture,
		"initial_aperture": initialAperture,
	})
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusCreated, "create fixture gate"); err != nil {
		return nil, err
	}
	gate, err := objectField(result.Body, "gate")
	if err != nil {
		return nil, fmt.Errorf("create fixture gate response: %w", err)
	}
	return gate, nil
}

func fixtureCommand(
	client *apiClient,
	gateID string,
	targetAperture float64,
	reason string,
	requestedBy string,
) (map[string]any, error) {
	result, err := client.post("/commands", map[string]any{
		"gate_id":         gateID,
		"target_aperture": targetAperture,
		"reason":          reason,
		"requested_by":    requestedBy,
	})
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusCreated, "create fixture command"); err != nil {
		return nil, err
	}
	command, err := objectField(result.Body, "command")
	if err != nil {
		return nil, fmt.Errorf("create fixture command response: %w", err)
	}
	return command, nil
}

func fixtureClearReview(
	client *apiClient,
	commandID string,
	verifier string,
) (map[string]any, error) {
	result, err := client.post("/commands/"+commandID+"/interlock", map[string]any{
		"verdict":  "clear",
		"verifier": verifier,
		"note":     "联锁条件满足",
	})
	if err != nil {
		return nil, err
	}
	if err := requireStatus(result, http.StatusOK, "clear fixture interlock"); err != nil {
		return nil, err
	}
	command, err := objectField(result.Body, "command")
	if err != nil {
		return nil, fmt.Errorf("clear fixture interlock response: %w", err)
	}
	return command, nil
}

// fixtureFaultedGate registers a gate, submits and clears a command, then
// executes it with simulate_fault so the gate is latched faulted. It returns
// the faulting command id, the faulted gate view and the target aperture.
func fixtureFaultedGate(
	client *apiClient,
	gateID string,
	targetAperture float64,
) (string, map[string]any, float64, error) {
	if _, err := fixtureGate(client, gateID, gateID+"-name", 5, 0.25); err != nil {
		return "", nil, 0, err
	}
	command, err := fixtureCommand(client, gateID, targetAperture, "故障处置流程验证", "dispatcher-lin")
	if err != nil {
		return "", nil, 0, err
	}
	commandID, err := stringField(command, "id")
	if err != nil {
		return "", nil, 0, err
	}
	approved, err := fixtureClearReview(client, commandID, "safety-wu")
	if err != nil {
		return "", nil, 0, err
	}
	token, err := stringField(approved, "execution_token")
	if err != nil {
		return "", nil, 0, err
	}
	faulted, err := client.post("/commands/"+commandID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          token,
		"simulate_fault": true,
	})
	if err != nil {
		return "", nil, 0, err
	}
	if err := requireStatus(faulted, http.StatusOK, "simulate execution fault"); err != nil {
		return "", nil, 0, err
	}
	gate, err := objectField(faulted.Body, "gate")
	if err != nil {
		return "", nil, 0, err
	}
	return commandID, gate, targetAperture, nil
}
