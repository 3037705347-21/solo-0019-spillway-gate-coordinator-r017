package storage

import (
	"context"
	"sync"
	"time"

	"example.com/spillway-gate-coordinator/internal/domain"
)

type MemoryRepository struct {
	mu       sync.RWMutex
	gates    map[string]domain.Gate
	commands map[string]domain.GateCommand
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		gates:    make(map[string]domain.Gate),
		commands: make(map[string]domain.GateCommand),
	}
}

func (m *MemoryRepository) CreateGate(_ context.Context, gate domain.Gate) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.gates[gate.ID]; exists {
		return domain.Conflict("duplicate_gate_id", "gate id already exists")
	}
	m.gates[gate.ID] = gate
	return nil
}

func (m *MemoryRepository) Gate(_ context.Context, id string) (domain.Gate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	gate, exists := m.gates[id]
	if !exists {
		return domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}
	return cloneGate(gate), nil
}

func (m *MemoryRepository) CreateCommand(_ context.Context, command domain.GateCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.NotFound("gate_not_found", "gate id was not found")
	}
	if gate.Status == domain.GateFaulted {
		return domain.Conflict("gate_faulted", "faulted gates cannot receive new commands")
	}
	if command.StartingAperture != gate.Aperture {
		return domain.Conflict("gate_aperture_changed", "gate aperture changed before the command was stored")
	}
	if command.TargetAperture > gate.MaxAperture {
		return domain.Invalid("invalid_target_aperture", "target aperture is outside the gate limit")
	}
	if _, exists := m.commands[command.ID]; exists {
		return domain.Conflict("duplicate_command_id", "command id already exists")
	}

	m.commands[command.ID] = command
	return nil
}

func (m *MemoryRepository) Command(_ context.Context, id string) (domain.GateCommand, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	command, exists := m.commands[id]
	if !exists {
		return domain.GateCommand{}, domain.NotFound("command_not_found", "command id was not found")
	}
	return cloneCommand(command), nil
}

func (m *MemoryRepository) ReviewCommand(
	_ context.Context,
	commandID string,
	decision domain.InterlockDecision,
	token string,
	now time.Time,
) (domain.GateCommand, domain.Gate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	command, exists := m.commands[commandID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("command_not_found", "command id was not found")
	}
	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}

	nextCommand := cloneCommand(command)
	if err := nextCommand.Review(decision, token, now); err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	nextGate := cloneGate(gate)
	if decision.IsClear() {
		if err := nextGate.Reserve(nextCommand.ID, now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
	}

	m.commands[nextCommand.ID] = nextCommand
	m.gates[nextGate.ID] = nextGate
	return cloneCommand(nextCommand), cloneGate(nextGate), nil
}

func (m *MemoryRepository) ExecuteCommand(
	_ context.Context,
	commandID string,
	token string,
	operator string,
	simulateFault bool,
	now time.Time,
) (domain.GateCommand, domain.Gate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	command, exists := m.commands[commandID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("command_not_found", "command id was not found")
	}
	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}

	nextCommand := cloneCommand(command)
	if err := nextCommand.BeginExecution(token, operator, now); err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	nextGate := cloneGate(gate)
	if err := nextGate.StartMovement(nextCommand.ID, now); err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	if simulateFault {
		if err := nextCommand.Abort("simulated_gate_fault", now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
		if err := nextGate.LatchFault(nextCommand.ID, nextCommand.TerminalReason, nextCommand.TargetAperture, now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
	} else {
		if err := nextGate.Settle(nextCommand.TargetAperture, now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
		if err := nextCommand.Complete(now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
	}

	m.commands[nextCommand.ID] = nextCommand
	m.gates[nextGate.ID] = nextGate
	return cloneCommand(nextCommand), cloneGate(nextGate), nil
}

func (m *MemoryRepository) ClearGateFault(
	_ context.Context,
	gateID string,
	evidence domain.ClearanceEvidence,
) (domain.Gate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	gate, exists := m.gates[gateID]
	if !exists {
		return domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}

	// The fault fact must be backed by a stored command that is terminally
	// aborted; re-verifying it inside the lock prevents clearing a gate whose
	// fault record does not correspond to a real finished failure, and
	// guarantees no approved/executing command still occupies the gate.
	if gate.Status == domain.GateFaulted {
		if gate.Fault == nil {
			return domain.Gate{}, domain.Conflict("fault_fact_missing", "fault fact is not established")
		}
		faultCommand, exists := m.commands[gate.Fault.CommandID]
		if !exists {
			return domain.Gate{}, domain.Conflict("fault_fact_missing", "fault command was not found")
		}
		if faultCommand.Status != domain.CommandAborted {
			return domain.Gate{}, domain.Conflict("fault_not_settled", "fault command has not terminated as aborted")
		}
		for _, command := range m.commands {
			if command.GateID != gateID {
				continue
			}
			if command.Status == domain.CommandApproved || command.Status == domain.CommandExecuting {
				return domain.Gate{}, domain.Conflict("gate_occupied", "an unfinished command still occupies the gate")
			}
		}
	}

	nextGate := cloneGate(gate)
	if err := nextGate.ClearFault(evidence); err != nil {
		return domain.Gate{}, err
	}

	m.gates[nextGate.ID] = nextGate
	return cloneGate(nextGate), nil
}

// GateClearanceStatus returns the gate under a single read lock so that the
// clearance eligibility report and the gate snapshot come from one
// consistent read; callers never have to stitch multiple reads together.
func (m *MemoryRepository) GateClearanceStatus(_ context.Context, gateID string) (domain.Gate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	gate, exists := m.gates[gateID]
	if !exists {
		return domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}
	return cloneGate(gate), nil
}

func cloneGate(gate domain.Gate) domain.Gate {
	if gate.Fault != nil {
		fault := *gate.Fault
		gate.Fault = &fault
	}
	if gate.LastClearance != nil {
		evidence := *gate.LastClearance
		gate.LastClearance = &evidence
	}
	return gate
}

func cloneCommand(command domain.GateCommand) domain.GateCommand {
	if command.Interlock == nil {
		return command
	}
	decision := *command.Interlock
	command.Interlock = &decision
	return command
}
