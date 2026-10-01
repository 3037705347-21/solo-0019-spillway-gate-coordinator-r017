package bootstrap

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	server := httptest.NewServer(NewHandler())
	return server, server.Close
}

func postJSON(t *testing.T, server *httptest.Server, path string, payload string) (*http.Response, map[string]any) {
	t.Helper()
	response, err := http.Post(server.URL+path, "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer response.Body.Close()
	body := map[string]any{}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response, body
}

func TestClearGateFaultEndToEnd(t *testing.T) {
	server, closeFn := newTestServer(t)
	defer closeFn()

	_, registerBody := postJSON(t, server, "/gates", `{"id":"gate-f","name":"故障闸","max_aperture":4,"initial_aperture":0.5}`)
	if _, ok := registerBody["gate"]; !ok {
		t.Fatalf("register gate failed: %v", registerBody)
	}
	_, commandBody := postJSON(t, server, "/commands", `{"gate_id":"gate-f","target_aperture":2.0,"reason":"x","requested_by":"dispatcher-lin"}`)
	command := commandBody["command"].(map[string]any)
	commandID := command["id"].(string)

	_, reviewBody := postJSON(t, server, "/commands/"+commandID+"/interlock", `{"verdict":"clear","verifier":"safety-wu","note":"ok"}`)
	reviewCommand := reviewBody["command"].(map[string]any)
	token := reviewCommand["execution_token"].(string)

	_, faultBody := postJSON(t, server, "/commands/"+commandID+"/execute",
		`{"operator":"operator-chen","token":"`+token+`","simulate_fault":true}`)
	faultGate := faultBody["gate"].(map[string]any)
	if faultGate["status"] != "faulted" {
		t.Fatalf("expected faulted gate, got %v", faultGate["status"])
	}

	// GET reports clearable conditions before the clearance.
	getResponse, err := http.Get(server.URL + "/gates/gate-f")
	if err != nil {
		t.Fatalf("GET gate: %v", err)
	}
	defer getResponse.Body.Close()
	var getBody struct {
		Gate struct {
			Status               string `json:"status"`
			ClearanceEligibility struct {
				Eligible         bool     `json:"eligible"`
				FaultEstablished bool     `json:"fault_established"`
				HasOccupancy     bool     `json:"has_occupancy"`
				Reasons          []string `json:"reasons"`
			} `json:"clearance_eligibility"`
		} `json:"gate"`
	}
	if err := json.NewDecoder(getResponse.Body).Decode(&getBody); err != nil {
		t.Fatalf("decode gate: %v", err)
	}
	if !getBody.Gate.ClearanceEligibility.Eligible || getBody.Gate.ClearanceEligibility.FaultEstablished != true || getBody.Gate.ClearanceEligibility.HasOccupancy {
		t.Fatalf("unexpected eligibility: %+v", getBody.Gate.ClearanceEligibility)
	}

	// Forbidden maps to HTTP 403 with a stable code.
	response, forbiddenBody := postJSON(t, server, "/gates/gate-f/clearance",
		`{"handled_by":"operator-chen","reference":"WO-1"}`)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("forbidden: expected 403, got %d", response.StatusCode)
	}
	if forbiddenBody["code"] != "maintainer_not_authorized" {
		t.Fatalf("unexpected forbidden code: %v", forbiddenBody["code"])
	}

	// Bad evidence maps to HTTP 400.
	response, badBody := postJSON(t, server, "/gates/gate-f/clearance",
		`{"handled_by":"maintainer-lu"}`)
	if response.StatusCode != http.StatusBadRequest || badBody["code"] != "invalid_clearance_reference" {
		t.Fatalf("bad evidence: expected 400/invalid_clearance_reference, got %d/%v", response.StatusCode, badBody["code"])
	}

	// Authorized clearance maps to HTTP 200.
	response, clearedBody := postJSON(t, server, "/gates/gate-f/clearance",
		`{"handled_by":"maintainer-lu","reference":"WO-20260930-17","note":"更换传感器"}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("clearance: expected 200, got %d body %v", response.StatusCode, clearedBody)
	}
	clearedGate := clearedBody["gate"].(map[string]any)
	if clearedGate["status"] != "idle" || clearedGate["aperture"].(float64) != 2.0 {
		t.Fatalf("cleared gate mismatch: %v", clearedGate)
	}
	clearance := clearedBody["clearance"].(map[string]any)
	if clearance["reference"] != "WO-20260930-17" {
		t.Fatalf("clearance reference mismatch: %v", clearance["reference"])
	}
	lastClearance := clearedGate["last_clearance"].(map[string]any)
	if lastClearance["cleared_at"] != clearance["cleared_at"] {
		t.Fatalf("response clearance timestamp %v disagrees with gate %v", clearance["cleared_at"], lastClearance["cleared_at"])
	}

	// Duplicate clearance maps to HTTP 409 and leaves the gate idle.
	response, duplicateBody := postJSON(t, server, "/gates/gate-f/clearance",
		`{"handled_by":"maintainer-lu","reference":"WO-18"}`)
	if response.StatusCode != http.StatusConflict || duplicateBody["code"] != "gate_not_faulted" {
		t.Fatalf("duplicate: expected 409/gate_not_faulted, got %d/%v", response.StatusCode, duplicateBody["code"])
	}
}

func TestClearGateFaultRejectsUnknownFields(t *testing.T) {
	server, closeFn := newTestServer(t)
	defer closeFn()

	postJSON(t, server, "/gates", `{"id":"gate-u","name":"闸","max_aperture":4,"initial_aperture":0}`)
	response, body := postJSON(t, server, "/gates/gate-u/clearance",
		`{"handled_by":"maintainer-lu","reference":"WO-1","unexpected":true}`)
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(body["code"].(string), "invalid_json") {
		t.Fatalf("unknown field: expected 400/invalid_json, got %d/%v", response.StatusCode, body["code"])
	}
}
