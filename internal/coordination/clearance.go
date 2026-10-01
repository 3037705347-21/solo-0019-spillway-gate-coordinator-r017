package coordination

import (
	"context"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
)

func (s *Service) ClearGateFault(
	ctx context.Context,
	gateID string,
	request contracts.ClearGateFaultRequest,
) (domain.GateCommand, domain.Gate, domain.ClearanceRecord, error) {
	clearance, err := domain.NewClearanceRecord(
		request.CommandID,
		request.Handler,
		request.Evidence,
		request.Note,
		s.now(),
	)
	if err != nil {
		return domain.GateCommand{}, domain.Gate{}, domain.ClearanceRecord{}, err
	}

	command, gate, err := s.repository.ClearGateFault(ctx, gateID, clearance, s.now())
	if err != nil {
		return domain.GateCommand{}, domain.Gate{}, domain.ClearanceRecord{}, err
	}
	return command, gate, clearance, nil
}
