package coordination

import (
	"context"
	"testing"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
	"example.com/spillway-gate-coordinator/internal/storage"
)

func faultedGateThroughWorkflow(t *testing.T, service *Service, ctx context.Context, gateID string) string {
	t.Helper()

	if _, err := service.RegisterGate(ctx, contracts.RegisterGateRequest{
		ID: gateID, Name: "故障闸", MaxAperture: 4, InitialAperture: 0.5,
	}); err != nil {
		t.Fatalf("RegisterGate: %v", err)
	}
	command, err := service.RequestCommand(ctx, contracts.RequestCommandRequest{
		GateID: gateID, TargetAperture: 2.0, Reason: "故障演练", RequestedBy: "dispatcher-lin",
	})
	if err != nil {
		t.Fatalf("RequestCommand: %v", err)
	}
	approved, _, err := service.ReviewInterlock(ctx, command.ID, contracts.ReviewInterlockRequest{
		Verdict: "clear", Verifier: "safety-wu", Note: "ok",
	})
	if err != nil {
		t.Fatalf("ReviewInterlock: %v", err)
	}
	aborted, faulted, err := service.ExecuteCommand(ctx, command.ID, contracts.ExecuteCommandRequest{
		Operator: "operator-chen", Token: approved.ExecutionToken, SimulateFault: true,
	})
	if err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
	if aborted.Status != domain.CommandAborted || faulted.Status != domain.GateFaulted {
		t.Fatalf("expected aborted/faulted, got %s/%s", aborted.Status, faulted.Status)
	}
	return aborted.ID
}

func TestClearGateFaultAuthorization(t *testing.T) {
	repo := storage.NewMemoryRepository()
	service := NewServiceWithOptions(repo, []string{"maintainer-lu"})
	ctx := context.Background()
	commandID := faultedGateThroughWorkflow(t, service, ctx, "gate-a")

	cases := []struct {
		name      string
		handledBy string
		reference string
		wantKind  domain.ErrorKind
		wantCode  string
	}{
		{"empty handler", "  ", "WO-1", domain.KindInvalid, "invalid_handled_by"},
		{"missing reference", "maintainer-lu", "  ", domain.KindInvalid, "invalid_clearance_reference"},
		{"unauthorized operator", "operator-chen", "WO-1", domain.KindForbidden, "maintainer_not_authorized"},
		{"unknown identity", "someone-else", "WO-1", domain.KindForbidden, "maintainer_not_authorized"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := service.ClearGateFault(ctx, "gate-a", contracts.ClearGateFaultRequest{
				HandledBy: tc.handledBy,
				Reference: tc.reference,
				Note:      "note",
			})
			if err == nil {
				t.Fatalf("expected %s error", tc.wantCode)
			}
			if domain.ErrorKindOf(err) != tc.wantKind || domain.ErrorCodeOf(err) != tc.wantCode {
				t.Fatalf("expected %s/%s, got %s/%s", tc.wantKind, tc.wantCode, domain.ErrorKindOf(err), domain.ErrorCodeOf(err))
			}
		})
	}

	// No failed attempt may have touched the gate or the aborted command.
	readGate, _ := repo.Gate(ctx, "gate-a")
	if readGate.Status != domain.GateFaulted || readGate.LastClearance != nil {
		t.Fatalf("gate changed after rejected clearance attempts: %+v", readGate)
	}
	readCommand, _ := repo.Command(ctx, commandID)
	if readCommand.Status != domain.CommandAborted {
		t.Fatalf("aborted command must remain terminal, got %s", readCommand.Status)
	}
}

func TestClearGateFaultSuccessRoundTrip(t *testing.T) {
	repo := storage.NewMemoryRepository()
	service := NewServiceWithOptions(repo, []string{"maintainer-lu"})
	ctx := context.Background()
	commandID := faultedGateThroughWorkflow(t, service, ctx, "gate-b")

	cleared, evidence, err := service.ClearGateFault(ctx, "gate-b", contracts.ClearGateFaultRequest{
		HandledBy: "  maintainer-lu  ",
		Reference: "  WO-42  ",
		Note:      " 现场处置 ",
	})
	if err != nil {
		t.Fatalf("ClearGateFault: %v", err)
	}
	if cleared.Status != domain.GateIdle || cleared.Aperture != 2.0 {
		t.Fatalf("cleared gate mismatch: %+v", cleared)
	}
	if evidence.Reference != "WO-42" {
		t.Fatalf("evidence reference mismatch: %q", evidence.Reference)
	}

	// The response evidence must be exactly what a later gate read returns.
	readGate, eligibility, err := service.GateClearanceStatus(ctx, "gate-b")
	if err != nil {
		t.Fatalf("GateClearanceStatus: %v", err)
	}
	if readGate.LastClearance == nil || *readGate.LastClearance != evidence {
		t.Fatalf("read-back evidence mismatch: %+v vs %+v", readGate.LastClearance, evidence)
	}
	if eligibility.Eligible {
		t.Fatalf("idle gate must not remain eligible: %+v", eligibility)
	}
	if readGate.Fault == nil || readGate.Fault.CommandID != commandID {
		t.Fatalf("fault history must remain readable: %+v", readGate.Fault)
	}

	// A new command after recovery must start from the retained aperture.
	recovered, err := service.RequestCommand(ctx, contracts.RequestCommandRequest{
		GateID: "gate-b", TargetAperture: 1.0, Reason: "恢复调度", RequestedBy: "dispatcher-lin",
	})
	if err != nil {
		t.Fatalf("RequestCommand after recovery: %v", err)
	}
	if recovered.StartingAperture != 2.0 {
		t.Fatalf("starting aperture: expected retained 2.0, got %v", recovered.StartingAperture)
	}
}

func TestClearGateFaultNotFound(t *testing.T) {
	service := NewServiceWithOptions(storage.NewMemoryRepository(), []string{"maintainer-lu"})
	_, _, err := service.ClearGateFault(context.Background(), "missing", contracts.ClearGateFaultRequest{
		HandledBy: "maintainer-lu",
		Reference: "WO-1",
	})
	if err == nil || domain.ErrorCodeOf(err) != "gate_not_found" {
		t.Fatalf("expected gate_not_found, got %v", err)
	}
}

func TestDefaultMaintainers(t *testing.T) {
	maintainers := DefaultMaintainers()
	if len(maintainers) == 0 {
		t.Fatal("default maintainer allow list must not be empty")
	}
	maintainers[0] = "mutated"
	if DefaultMaintainers()[0] == "mutated" {
		t.Fatal("DefaultMaintainers must return an independent copy")
	}
}
