package storage

import (
	"context"
	"sync"
	"testing"
	"time"

	"example.com/spillway-gate-coordinator/internal/domain"
)

func setupFaultedGate(t *testing.T, repo *MemoryRepository, gateID string) (domain.Gate, domain.GateCommand) {
	t.Helper()
	ctx := context.Background()
	now := time.Unix(10, 0)

	gate, err := domain.NewGate(gateID, "故障闸", 4, 0.5, now)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	if err := repo.CreateGate(ctx, gate); err != nil {
		t.Fatalf("CreateGate: %v", err)
	}

	command, err := domain.NewGateCommand("cmd-1", gateID, 2.0, 0.5, 4, "故障演练", "dispatcher-lin", now)
	if err != nil {
		t.Fatalf("NewGateCommand: %v", err)
	}
	if err := repo.CreateCommand(ctx, command); err != nil {
		t.Fatalf("CreateCommand: %v", err)
	}

	decision, err := domain.NewInterlockDecision("clear", "safety-wu", "ok", now)
	if err != nil {
		t.Fatalf("NewInterlockDecision: %v", err)
	}
	approved, reserved, err := repo.ReviewCommand(ctx, "cmd-1", decision, "token-1", now)
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if approved.Status != domain.CommandApproved || reserved.Status != domain.GateReserved {
		t.Fatalf("expected approved/reserved, got %s/%s", approved.Status, reserved.Status)
	}

	aborted, faulted, err := repo.ExecuteCommand(ctx, "cmd-1", "token-1", "operator-chen", true, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ExecuteCommand fault: %v", err)
	}
	if aborted.Status != domain.CommandAborted || faulted.Status != domain.GateFaulted {
		t.Fatalf("expected aborted/faulted, got %s/%s", aborted.Status, faulted.Status)
	}
	return faulted, aborted
}

func TestClearGateFaultSuccess(t *testing.T) {
	repo := NewMemoryRepository()
	faulted, aborted := setupFaultedGate(t, repo, "gate-a")
	version := faulted.Version

	evidence := domain.ClearanceEvidence{
		HandledBy: "maintainer-lu",
		Reference: "WO-1",
		Note:      "现场处置",
		ClearedAt: time.Unix(20, 0),
	}
	cleared, err := repo.ClearGateFault(context.Background(), "gate-a", evidence)
	if err != nil {
		t.Fatalf("ClearGateFault: %v", err)
	}
	if cleared.Status != domain.GateIdle {
		t.Fatalf("status: expected idle, got %s", cleared.Status)
	}
	if cleared.Aperture != 2.0 {
		t.Fatalf("retained aperture: expected 2.0, got %v", cleared.Aperture)
	}
	if cleared.ActiveCommandID != "" {
		t.Fatalf("active command must stay empty, got %q", cleared.ActiveCommandID)
	}
	if cleared.Version != version+1 {
		t.Fatalf("version: expected %d, got %d", version+1, cleared.Version)
	}
	if cleared.LastClearance == nil || cleared.LastClearance.Reference != "WO-1" {
		t.Fatalf("clearance not recorded: %+v", cleared.LastClearance)
	}
	if cleared.Fault == nil || cleared.Fault.CommandID != aborted.ID {
		t.Fatalf("fault record must be retained: %+v", cleared.Fault)
	}

	// The aborted command stays terminal and never gets rewritten.
	stored, err := repo.Command(context.Background(), aborted.ID)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if stored.Status != domain.CommandAborted || stored.ExecutionToken != "" {
		t.Fatalf("aborted command mismatch: %+v", stored)
	}
}

func TestClearGateFaultFailuresLeaveStateUntouched(t *testing.T) {
	repo := NewMemoryRepository()
	faulted, _ := setupFaultedGate(t, repo, "gate-b")

	cases := []struct {
		name     string
		gateID   string
		evidence domain.ClearanceEvidence
		code     string
	}{
		{
			name:     "unknown gate",
			gateID:   "missing",
			evidence: domain.ClearanceEvidence{HandledBy: "m", Reference: "WO", ClearedAt: time.Unix(20, 0)},
			code:     "gate_not_found",
		},
		{
			name:     "missing reference",
			gateID:   "gate-b",
			evidence: domain.ClearanceEvidence{HandledBy: "m", ClearedAt: time.Unix(20, 0)},
			code:     "invalid_clearance_reference",
		},
		{
			name:     "missing handler",
			gateID:   "gate-b",
			evidence: domain.ClearanceEvidence{Reference: "WO", ClearedAt: time.Unix(20, 0)},
			code:     "invalid_handled_by",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repo.ClearGateFault(context.Background(), tc.gateID, tc.evidence); err == nil || domain.ErrorCodeOf(err) != tc.code {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
			gate, err := repo.Gate(context.Background(), "gate-b")
			if err != nil {
				t.Fatalf("Gate: %v", err)
			}
			if gate.Status != domain.GateFaulted || gate.Version != faulted.Version {
				t.Fatalf("gate changed after failed clearance: status=%s version=%d", gate.Status, gate.Version)
			}
			if gate.LastClearance != nil {
				t.Fatalf("failed clearance must not record evidence")
			}
		})
	}
}

func TestClearGateFaultRejectsBypassOnNonFaultedGate(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()
	gate, err := domain.NewGate("gate-c", "空闲闸", 4, 0, time.Unix(1, 0))
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	if err := repo.CreateGate(ctx, gate); err != nil {
		t.Fatalf("CreateGate: %v", err)
	}

	evidence := domain.ClearanceEvidence{HandledBy: "m", Reference: "WO", ClearedAt: time.Unix(2, 0)}
	if _, err := repo.ClearGateFault(ctx, "gate-c", evidence); err == nil || domain.ErrorCodeOf(err) != "gate_not_faulted" {
		t.Fatalf("direct clearance of an idle gate must conflict, got %v", err)
	}
}

func TestClearGateFaultRejectsOccupancy(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()

	// Build a gate that is faulted with a second, still-approved command
	// pointing at it, simulating a state where an unfinished occupancy exists.
	faulted, _ := setupFaultedGate(t, repo, "gate-d")

	other, err := domain.NewGateCommand("cmd-other", "gate-d", 0.25, 2.0, 4, "占用命令", "dispatcher-lin", time.Unix(11, 0))
	if err != nil {
		t.Fatalf("NewGateCommand: %v", err)
	}
	other.Status = domain.CommandApproved
	other.ExecutionToken = "token-other"
	// White-box insertion: a normal create is refused on a faulted gate, and
	// this scenario models an in-flight occupant that must block clearance.
	repo.mu.Lock()
	repo.commands["cmd-other"] = other
	repo.mu.Unlock()

	evidence := domain.ClearanceEvidence{HandledBy: "m", Reference: "WO", ClearedAt: time.Unix(20, 0)}
	if _, err := repo.ClearGateFault(ctx, "gate-d", evidence); err == nil || domain.ErrorCodeOf(err) != "gate_occupied" {
		t.Fatalf("clearance with an approved occupant must conflict, got %v", err)
	}

	gate, _ := repo.Gate(ctx, "gate-d")
	if gate.Status != domain.GateFaulted || gate.Version != faulted.Version {
		t.Fatalf("gate changed after occupied clearance: status=%s version=%d", gate.Status, gate.Version)
	}
}

func TestClearGateFaultConcurrentSingleWinner(t *testing.T) {
	repo := NewMemoryRepository()
	faulted, _ := setupFaultedGate(t, repo, "gate-e")

	const contenders = 16
	var wg sync.WaitGroup
	results := make(chan error, contenders)
	start := make(chan struct{})
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			evidence := domain.ClearanceEvidence{
				HandledBy: "maintainer-lu",
				Reference: "WO-concurrent",
				ClearedAt: time.Unix(30, 0),
			}
			_, err := repo.ClearGateFault(context.Background(), "gate-e", evidence)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case domain.ErrorCodeOf(err) == "gate_not_faulted":
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != contenders-1 {
		t.Fatalf("expected exactly one success and %d conflicts, got %d/%d", contenders-1, successes, conflicts)
	}

	gate, _ := repo.Gate(context.Background(), "gate-e")
	if gate.Status != domain.GateIdle || gate.Version != faulted.Version+1 {
		t.Fatalf("cleared gate mismatch: status=%s version=%d", gate.Status, gate.Version)
	}
}
