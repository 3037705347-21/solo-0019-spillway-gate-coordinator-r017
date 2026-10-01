package coordination

import (
	"context"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
)

// ClearGateFault runs the fault clearance workflow: the handling basis is
// validated, the submitter must be an authorized maintainer, and the guarded
// faulted -> idle transition is committed atomically inside the repository.
// It returns the cleared gate and the recorded evidence so callers can verify
// that the command, gate, and subsequent reads tell the same story.
func (s *Service) ClearGateFault(
	ctx context.Context,
	gateID string,
	request contracts.ClearGateFaultRequest,
) (domain.Gate, domain.ClearanceEvidence, error) {
	evidence, err := domain.NewClearanceEvidence(
		request.HandledBy,
		request.Reference,
		request.Note,
		s.now(),
	)
	if err != nil {
		return domain.Gate{}, domain.ClearanceEvidence{}, err
	}
	if err := s.authorizeMaintainer(evidence.HandledBy); err != nil {
		return domain.Gate{}, domain.ClearanceEvidence{}, err
	}

	gate, err := s.repository.ClearGateFault(ctx, gateID, evidence)
	if err != nil {
		return domain.Gate{}, domain.ClearanceEvidence{}, err
	}
	// Return the persisted record (not the input) so the response cannot
	// disagree with what later reads of the gate expose.
	if gate.LastClearance == nil {
		return domain.Gate{}, domain.ClearanceEvidence{}, domain.Internal(
			"clearance_not_recorded",
			"clearance succeeded but was not recorded",
		)
	}
	return gate, *gate.LastClearance, nil
}

// GateClearanceStatus returns a gate snapshot together with its clearance
// eligibility report. The two are derived from the same locked read, so the
// report can never disagree with the returned gate.
func (s *Service) GateClearanceStatus(ctx context.Context, gateID string) (domain.Gate, domain.ClearanceEligibility, error) {
	gate, err := s.repository.GateClearanceStatus(ctx, gateID)
	if err != nil {
		return domain.Gate{}, domain.ClearanceEligibility{}, err
	}
	return gate, gate.ClearanceEligibility(), nil
}
