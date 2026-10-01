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

// fixtureFaultedGate drives a command through review and a fault-simulating
// execution so the target gate ends latched in faulted status with the
// command aborted. It returns the faulted gate and the aborted command.
func fixtureFaultedGate(
	client *apiClient,
	gateID string,
	targetAperture float64,
) (map[string]any, map[string]any, error) {
	command, err := fixtureCommand(client, gateID, targetAperture, "故障演练", "dispatcher-lin")
	if err != nil {
		return nil, nil, err
	}
	commandID, err := stringField(command, "id")
	if err != nil {
		return nil, nil, err
	}
	approved, err := fixtureClearReview(client, commandID, "safety-wu")
	if err != nil {
		return nil, nil, err
	}
	token, err := stringField(approved, "execution_token")
	if err != nil {
		return nil, nil, err
	}

	faulted, err := client.post("/commands/"+commandID+"/execute", map[string]any{
		"operator":       "operator-chen",
		"token":          token,
		"simulate_fault": true,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := requireStatus(faulted, http.StatusOK, "execute command with simulated fault"); err != nil {
		return nil, nil, err
	}
	gate, err := objectField(faulted.Body, "gate")
	if err != nil {
		return nil, nil, err
	}
	abortedCommand, err := objectField(faulted.Body, "command")
	if err != nil {
		return nil, nil, err
	}
	return gate, abortedCommand, nil
}
