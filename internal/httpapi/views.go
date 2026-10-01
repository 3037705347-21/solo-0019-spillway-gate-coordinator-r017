package httpapi

import (
	"time"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
)

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func gateView(gate domain.Gate) contracts.GateView {
	eligibility := gate.ClearanceEligibility()
	view := contracts.GateView{
		ID:              gate.ID,
		Name:            gate.Name,
		Status:          string(gate.Status),
		Aperture:        gate.Aperture,
		MaxAperture:     gate.MaxAperture,
		ActiveCommandID: gate.ActiveCommandID,
		ClearanceEligibility: contracts.ClearanceEligibilityView{
			Eligible:         eligibility.Eligible,
			FaultEstablished: eligibility.FaultEstablished,
			HasOccupancy:     eligibility.HasOccupancy,
			Reasons:          eligibility.Reasons,
		},
		UpdatedAt: formatTime(gate.UpdatedAt),
		Version:   gate.Version,
	}
	if gate.Fault != nil {
		view.Fault = &contracts.FaultRecordView{
			CommandID:  gate.Fault.CommandID,
			Reason:     gate.Fault.Reason,
			Aperture:   gate.Fault.Aperture,
			OccurredAt: formatTime(gate.Fault.OccurredAt),
		}
	}
	if gate.LastClearance != nil {
		view.LastClearance = clearanceEvidenceView(*gate.LastClearance)
	}
	return view
}

func clearanceEvidenceView(evidence domain.ClearanceEvidence) *contracts.ClearanceEvidenceView {
	return &contracts.ClearanceEvidenceView{
		HandledBy: evidence.HandledBy,
		Reference: evidence.Reference,
		Note:      evidence.Note,
		ClearedAt: formatTime(evidence.ClearedAt),
	}
}

func commandView(command domain.GateCommand, includeToken bool) contracts.CommandView {
	view := contracts.CommandView{
		ID:               command.ID,
		GateID:           command.GateID,
		TargetAperture:   command.TargetAperture,
		StartingAperture: command.StartingAperture,
		Reason:           command.Reason,
		RequestedBy:      command.RequestedBy,
		Status:           string(command.Status),
		ExecutedBy:       command.ExecutedBy,
		TerminalReason:   command.TerminalReason,
		CreatedAt:        formatTime(command.CreatedAt),
		UpdatedAt:        formatTime(command.UpdatedAt),
		Version:          command.Version,
	}

	if includeToken && command.Status == domain.CommandApproved {
		view.ExecutionToken = command.ExecutionToken
	}
	if command.Interlock != nil {
		view.Interlock = &contracts.InterlockDecisionView{
			Verdict:    string(command.Interlock.Verdict),
			Verifier:   command.Interlock.Verifier,
			Note:       command.Interlock.Note,
			ReviewedAt: formatTime(command.Interlock.ReviewedAt),
		}
	}
	return view
}
