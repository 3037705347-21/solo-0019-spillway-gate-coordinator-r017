package domain

import (
	"regexp"
	"strings"
	"time"
)

type GateStatus string

const (
	GateIdle     GateStatus = "idle"
	GateReserved GateStatus = "reserved"
	GateMoving   GateStatus = "moving"
	GateFaulted  GateStatus = "faulted"
)

var gateIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,31}$`)

type Gate struct {
	ID              string
	Name            string
	Status          GateStatus
	Aperture        float64
	MaxAperture     float64
	ActiveCommandID string
	Fault           *FaultRecord
	LastClearance   *ClearanceRecord
	UpdatedAt       time.Time
	Version         int64
}

func NewGate(id, name string, maxAperture, initialAperture float64, now time.Time) (Gate, error) {
	id = strings.TrimSpace(id)
	if !gateIDPattern.MatchString(id) {
		return Gate{}, Invalid("invalid_gate_id", "gate id must use lowercase letters, digits, and hyphens")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return Gate{}, Invalid("invalid_gate_name", "gate name is required")
	}
	if maxAperture <= 0 || maxAperture > 1000 {
		return Gate{}, Invalid("invalid_max_aperture", "max aperture must be greater than zero")
	}
	if initialAperture < 0 || initialAperture > maxAperture {
		return Gate{}, Invalid("invalid_initial_aperture", "initial aperture must be within the gate limit")
	}

	return Gate{
		ID:          id,
		Name:        name,
		Status:      GateIdle,
		Aperture:    initialAperture,
		MaxAperture: maxAperture,
		UpdatedAt:   now.UTC(),
		Version:     1,
	}, nil
}

func (g *Gate) Reserve(commandID string, now time.Time) error {
	if g.Status != GateIdle {
		return Conflict("gate_not_idle", "gate is not available for a new command")
	}
	if g.ActiveCommandID != "" {
		return Conflict("gate_already_reserved", "gate already references an active command")
	}
	if strings.TrimSpace(commandID) == "" {
		return Invalid("invalid_active_command", "active command id is required")
	}

	g.Status = GateReserved
	g.ActiveCommandID = commandID
	g.UpdatedAt = now.UTC()
	g.Version++
	return nil
}

func (g *Gate) StartMovement(commandID string, now time.Time) error {
	if g.Status != GateReserved {
		return Conflict("gate_not_reserved", "gate must be reserved before movement starts")
	}
	if g.ActiveCommandID != commandID {
		return Conflict("gate_command_mismatch", "gate is reserved by a different command")
	}

	g.Status = GateMoving
	g.UpdatedAt = now.UTC()
	g.Version++
	return nil
}

func (g *Gate) Settle(finalAperture float64, now time.Time) error {
	if g.Status != GateMoving {
		return Conflict("gate_not_moving", "gate must be moving before it can settle")
	}
	if finalAperture < 0 || finalAperture > g.MaxAperture {
		return Invalid("invalid_final_aperture", "final aperture must be within the gate limit")
	}

	g.Aperture = finalAperture
	g.ActiveCommandID = ""
	g.Status = GateIdle
	g.UpdatedAt = now.UTC()
	g.Version++
	return nil
}

// LatchFault latches the gate into the faulted status after an execution
// failure and records the explicit fault fact (faulting command and reason).
// The active command reference is released, but the aperture is retained.
func (g *Gate) LatchFault(commandID, reason string, finalAperture float64, now time.Time) error {
	if g.Status != GateMoving {
		return Conflict("gate_not_moving", "gate must be moving before a fault can latch")
	}
	if g.ActiveCommandID != commandID {
		return Conflict("gate_command_mismatch", "gate is reserved by a different command")
	}
	if strings.TrimSpace(commandID) == "" {
		return Invalid("invalid_active_command", "faulting command id is required")
	}
	if finalAperture < 0 || finalAperture > g.MaxAperture {
		return Invalid("invalid_final_aperture", "final aperture must be within the gate limit")
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "gate_execution_failed"
	}
	g.Aperture = finalAperture
	g.ActiveCommandID = ""
	g.Fault = &FaultRecord{
		CommandID:  commandID,
		Reason:     reason,
		Aperture:   finalAperture,
		OccurredAt: now.UTC(),
	}
	g.LastClearance = nil
	g.Status = GateFaulted
	g.UpdatedAt = now.UTC()
	g.Version++
	return nil
}

// ClearFault is the single formal faulted -> idle transition. It is guarded:
// the gate must still be latched (rejecting duplicate and concurrent clears),
// the clearance must reference the exact faulting command, and that command
// must present an explicit, matching fault fact. Callers must independently
// guarantee no non-terminal command still references the gate (the repository
// performs that scan under the same lock before calling this method).
func (g *Gate) ClearFault(clearance ClearanceRecord, faultingCommand GateCommand, now time.Time) error {
	if g.Status != GateFaulted {
		return Conflict("gate_not_faulted", "only faulted gates can be cleared")
	}
	if g.Fault == nil {
		return Conflict("fault_fact_missing", "faulted gate has no recorded fault fact")
	}
	if clearance.FaultCommandID != g.Fault.CommandID {
		return Conflict("fault_command_mismatch", "clearance references a different command than the latched fault")
	}
	if faultingCommand.ID != g.Fault.CommandID || faultingCommand.GateID != g.ID {
		return Conflict("fault_command_mismatch", "faulting command does not reference the latched gate fault")
	}
	if faultingCommand.Status != CommandAborted {
		return Conflict("fault_fact_unclear", "fault fact is not explicit: faulting command is not aborted")
	}
	if strings.TrimSpace(faultingCommand.TerminalReason) == "" {
		return Conflict("fault_fact_unclear", "fault fact is not explicit: faulting command has no terminal reason")
	}
	if faultingCommand.TerminalReason != g.Fault.Reason {
		return Conflict("fault_fact_unclear", "fault fact is not explicit: command and gate fault reasons diverge")
	}

	g.Status = GateIdle
	g.ActiveCommandID = ""
	clearanceCopy := clearance
	g.LastClearance = &clearanceCopy
	g.UpdatedAt = now.UTC()
	g.Version++
	return nil
}
