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

// FaultRecord captures the latched fault fact: the aborted command that
// triggered the latch, its terminal reason, and the aperture retained on site.
type FaultRecord struct {
	CommandID  string
	Reason     string
	Aperture   float64
	OccurredAt time.Time
}

// ClearanceEvidence is the handling basis submitted by an authorized
// maintainer when requesting that a latched fault be released.
type ClearanceEvidence struct {
	HandledBy string
	Reference string
	Note      string
	ClearedAt time.Time
}

type Gate struct {
	ID              string
	Name            string
	Status          GateStatus
	Aperture        float64
	MaxAperture     float64
	ActiveCommandID string
	Fault           *FaultRecord
	LastClearance   *ClearanceEvidence
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

// LatchFault ends a movement by latching the gate into faulted status. The
// final aperture is retained and the active command reference is released so
// the aborted command can never keep owning the gate.
func (g *Gate) LatchFault(commandID, reason string, finalAperture float64, now time.Time) error {
	if g.Status != GateMoving {
		return Conflict("gate_not_moving", "gate must be moving before a fault can be latched")
	}
	if g.ActiveCommandID != commandID {
		return Conflict("gate_command_mismatch", "gate is reserved by a different command")
	}
	if strings.TrimSpace(commandID) == "" {
		return Invalid("invalid_active_command", "fault record requires the aborted command id")
	}
	if finalAperture < 0 || finalAperture > g.MaxAperture {
		return Invalid("invalid_final_aperture", "final aperture must be within the gate limit")
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "gate_execution_failed"
	}

	g.Aperture = finalAperture
	g.Fault = &FaultRecord{
		CommandID:  commandID,
		Reason:     reason,
		Aperture:   finalAperture,
		OccurredAt: now.UTC(),
	}
	g.ActiveCommandID = ""
	g.Status = GateFaulted
	g.UpdatedAt = now.UTC()
	g.Version++
	return nil
}

// ClearFault is the only formal transition out of the faulted latch. It is a
// guarded transition: the fault fact must be present, there must be no
// unfinished occupancy, and the handling basis must identify an authorized
// maintainer and a handling reference. The retained aperture is kept.
func (g *Gate) ClearFault(evidence ClearanceEvidence) error {
	if g.Status != GateFaulted {
		return Conflict("gate_not_faulted", "only faulted gates can be cleared")
	}
	if g.Fault == nil {
		return Conflict("fault_fact_missing", "fault fact is not established")
	}
	if g.ActiveCommandID != "" {
		return Conflict("gate_occupied", "gate still references an unfinished command")
	}
	if strings.TrimSpace(evidence.HandledBy) == "" {
		return Invalid("invalid_handled_by", "handled_by is required")
	}
	if strings.TrimSpace(evidence.Reference) == "" {
		return Invalid("invalid_clearance_reference", "handling reference is required as clearance evidence")
	}
	if evidence.ClearedAt.IsZero() {
		return Invalid("invalid_clearance_timestamp", "clearance timestamp is required")
	}

	evidenceCopy := ClearanceEvidence{
		HandledBy: strings.TrimSpace(evidence.HandledBy),
		Reference: strings.TrimSpace(evidence.Reference),
		Note:      strings.TrimSpace(evidence.Note),
		ClearedAt: evidence.ClearedAt.UTC(),
	}
	g.LastClearance = &evidenceCopy
	g.Status = GateIdle
	g.UpdatedAt = evidenceCopy.ClearedAt
	g.Version++
	return nil
}

// ClearanceEligibility reports whether a gate currently satisfies the
// preconditions for fault clearance, and why not when it does not. The
// report is derived from a single consistent gate snapshot.
type ClearanceEligibility struct {
	Eligible         bool
	FaultEstablished bool
	HasOccupancy     bool
	Reasons          []string
}

func (g Gate) ClearanceEligibility() ClearanceEligibility {
	report := ClearanceEligibility{
		FaultEstablished: g.Status == GateFaulted && g.Fault != nil,
		HasOccupancy:     g.ActiveCommandID != "",
		Reasons:          []string{},
	}

	switch {
	case g.Status != GateFaulted:
		report.Reasons = append(report.Reasons, "gate is not latched in faulted status")
	case g.Fault == nil:
		report.Reasons = append(report.Reasons, "fault fact is not established")
	}
	if g.ActiveCommandID != "" {
		report.Reasons = append(report.Reasons, "an unfinished command still occupies the gate")
	}

	report.Eligible = len(report.Reasons) == 0
	return report
}
