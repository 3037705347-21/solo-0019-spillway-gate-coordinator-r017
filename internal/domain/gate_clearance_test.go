package domain

import (
	"testing"
	"time"
)

func testFaultedGate(t *testing.T) Gate {
	t.Helper()
	gate, err := NewGate("gate-f", "故障闸", 4, 0.5, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	if err := gate.Reserve("cmd-1", time.Unix(2, 0)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := gate.StartMovement("cmd-1", time.Unix(3, 0)); err != nil {
		t.Fatalf("StartMovement: %v", err)
	}
	if err := gate.LatchFault("cmd-1", "simulated_gate_fault", 2.0, time.Unix(4, 0)); err != nil {
		t.Fatalf("LatchFault: %v", err)
	}
	return gate
}

func TestLatchFaultState(t *testing.T) {
	gate := testFaultedGate(t)

	if gate.Status != GateFaulted {
		t.Fatalf("status: expected faulted, got %s", gate.Status)
	}
	if gate.ActiveCommandID != "" {
		t.Fatalf("faulted gate must release the active command, got %q", gate.ActiveCommandID)
	}
	if gate.Aperture != 2.0 {
		t.Fatalf("retained aperture: expected 2.0, got %v", gate.Aperture)
	}
	if gate.Fault == nil || gate.Fault.CommandID != "cmd-1" || gate.Fault.Reason != "simulated_gate_fault" {
		t.Fatalf("fault record mismatch: %+v", gate.Fault)
	}
}

func TestLatchFaultRequiresMovingAndOwner(t *testing.T) {
	idle, err := NewGate("gate-i", "空闲闸", 4, 0, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	if err := idle.LatchFault("cmd-1", "x", 1, time.Unix(2, 0)); err == nil || ErrorCodeOf(err) != "gate_not_moving" {
		t.Fatalf("latching an idle gate must fail with gate_not_moving, got %v", err)
	}

	moving := testFaultedGate(t)
	_ = moving
	gate, _ := NewGate("gate-m", "运动闸", 4, 0, time.Unix(1, 0))
	if err := gate.Reserve("cmd-1", time.Unix(2, 0)); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := gate.StartMovement("cmd-1", time.Unix(3, 0)); err != nil {
		t.Fatalf("StartMovement: %v", err)
	}
	if err := gate.LatchFault("cmd-other", "x", 1, time.Unix(4, 0)); err == nil || ErrorCodeOf(err) != "gate_command_mismatch" {
		t.Fatalf("latching with another command must fail, got %v", err)
	}
}

func TestClearFaultGuardedTransition(t *testing.T) {
	gate := testFaultedGate(t)
	version := gate.Version

	evidence := ClearanceEvidence{
		HandledBy: "maintainer-lu",
		Reference: "WO-1",
		Note:      "现场处置完成",
		ClearedAt: time.Unix(5, 0),
	}
	if err := gate.ClearFault(evidence); err != nil {
		t.Fatalf("ClearFault: %v", err)
	}

	if gate.Status != GateIdle {
		t.Fatalf("status: expected idle, got %s", gate.Status)
	}
	if gate.Aperture != 2.0 {
		t.Fatalf("aperture must be retained at 2.0, got %v", gate.Aperture)
	}
	if gate.ActiveCommandID != "" {
		t.Fatalf("cleared gate must have no active command, got %q", gate.ActiveCommandID)
	}
	if gate.Version != version+1 {
		t.Fatalf("version: expected %d, got %d", version+1, gate.Version)
	}
	if !gate.UpdatedAt.Equal(time.Unix(5, 0)) {
		t.Fatalf("updated_at must equal clearance timestamp, got %v", gate.UpdatedAt)
	}
	if gate.LastClearance == nil || gate.LastClearance.Reference != "WO-1" {
		t.Fatalf("clearance evidence not recorded: %+v", gate.LastClearance)
	}
	if gate.Fault == nil || gate.Fault.CommandID != "cmd-1" {
		t.Fatalf("historical fault record must be retained: %+v", gate.Fault)
	}

	// faulted -> idle is one-way: a second clearance is a conflict.
	if err := gate.ClearFault(evidence); err == nil || ErrorCodeOf(err) != "gate_not_faulted" {
		t.Fatalf("repeated clearance must conflict, got %v", err)
	}
}

func TestClearFaultEvidenceValidation(t *testing.T) {
	cases := []struct {
		name     string
		evidence ClearanceEvidence
		code     string
	}{
		{"missing handler", ClearanceEvidence{Reference: "WO-1", ClearedAt: time.Unix(5, 0)}, "invalid_handled_by"},
		{"missing reference", ClearanceEvidence{HandledBy: "maintainer-lu", ClearedAt: time.Unix(5, 0)}, "invalid_clearance_reference"},
		{"missing timestamp", ClearanceEvidence{HandledBy: "maintainer-lu", Reference: "WO-1"}, "invalid_clearance_timestamp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gate := testFaultedGate(t)
			version := gate.Version
			err := gate.ClearFault(tc.evidence)
			if err == nil || ErrorCodeOf(err) != tc.code {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
			if gate.Status != GateFaulted || gate.Version != version || gate.LastClearance != nil {
				t.Fatalf("failed clearance must not change the gate: %+v", gate)
			}
		})
	}
}

func TestNewClearanceEvidence(t *testing.T) {
	evidence, err := NewClearanceEvidence("  maintainer-lu  ", "  WO-1  ", " note ", time.Unix(7, 0))
	if err != nil {
		t.Fatalf("NewClearanceEvidence: %v", err)
	}
	if evidence.HandledBy != "maintainer-lu" || evidence.Reference != "WO-1" || evidence.Note != "note" {
		t.Fatalf("evidence fields not normalized: %+v", evidence)
	}

	if _, err := NewClearanceEvidence("", "WO-1", "", time.Unix(1, 0)); err == nil || ErrorCodeOf(err) != "invalid_handled_by" {
		t.Fatalf("missing handler: %v", err)
	}
	if _, err := NewClearanceEvidence("m", "", "", time.Unix(1, 0)); err == nil || ErrorCodeOf(err) != "invalid_clearance_reference" {
		t.Fatalf("missing reference: %v", err)
	}
}

func TestClearanceEligibility(t *testing.T) {
	faulted := testFaultedGate(t)
	report := faulted.ClearanceEligibility()
	if !report.Eligible || !report.FaultEstablished || report.HasOccupancy {
		t.Fatalf("faulted gate without occupancy should be eligible: %+v", report)
	}
	if len(report.Reasons) != 0 {
		t.Fatalf("eligible report must carry no reasons, got %v", report.Reasons)
	}

	idle, _ := NewGate("gate-i", "空闲闸", 4, 0, time.Unix(1, 0))
	idleReport := idle.ClearanceEligibility()
	if idleReport.Eligible || idleReport.FaultEstablished {
		t.Fatalf("idle gate must not be eligible: %+v", idleReport)
	}
}
