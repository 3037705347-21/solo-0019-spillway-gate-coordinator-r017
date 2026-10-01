package contracts

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type InterlockDecisionView struct {
	Verdict    string `json:"verdict"`
	Verifier   string `json:"verifier"`
	Note       string `json:"note"`
	ReviewedAt string `json:"reviewed_at"`
}

type FaultRecordView struct {
	CommandID  string  `json:"command_id"`
	Reason     string  `json:"reason"`
	Aperture   float64 `json:"aperture"`
	OccurredAt string  `json:"occurred_at"`
}

type ClearanceEvidenceView struct {
	HandledBy string `json:"handled_by"`
	Reference string `json:"reference"`
	Note      string `json:"note,omitempty"`
	ClearedAt string `json:"cleared_at"`
}

type ClearanceEligibilityView struct {
	Eligible         bool     `json:"eligible"`
	FaultEstablished bool     `json:"fault_established"`
	HasOccupancy     bool     `json:"has_occupancy"`
	Reasons          []string `json:"reasons"`
}

type GateView struct {
	ID                   string                   `json:"id"`
	Name                 string                   `json:"name"`
	Status               string                   `json:"status"`
	Aperture             float64                  `json:"aperture"`
	MaxAperture          float64                  `json:"max_aperture"`
	ActiveCommandID      string                   `json:"active_command_id,omitempty"`
	Fault                *FaultRecordView         `json:"fault,omitempty"`
	LastClearance        *ClearanceEvidenceView   `json:"last_clearance,omitempty"`
	ClearanceEligibility ClearanceEligibilityView `json:"clearance_eligibility"`
	UpdatedAt            string                   `json:"updated_at"`
	Version              int64                    `json:"version"`
}

type CommandView struct {
	ID               string                 `json:"id"`
	GateID           string                 `json:"gate_id"`
	TargetAperture   float64                `json:"target_aperture"`
	StartingAperture float64                `json:"starting_aperture"`
	Reason           string                 `json:"reason"`
	RequestedBy      string                 `json:"requested_by"`
	Status           string                 `json:"status"`
	Interlock        *InterlockDecisionView `json:"interlock,omitempty"`
	ExecutionToken   string                 `json:"execution_token,omitempty"`
	ExecutedBy       string                 `json:"executed_by,omitempty"`
	TerminalReason   string                 `json:"terminal_reason,omitempty"`
	CreatedAt        string                 `json:"created_at"`
	UpdatedAt        string                 `json:"updated_at"`
	Version          int64                  `json:"version"`
}

type RegisterGateResponse struct {
	Gate GateView `json:"gate"`
}

type GateResponse struct {
	Gate GateView `json:"gate"`
}

type CommandResponse struct {
	Command CommandView `json:"command"`
}

type ReviewCommandResponse struct {
	Command CommandView `json:"command"`
	Gate    GateView    `json:"gate"`
}

type ExecuteCommandResponse struct {
	Command CommandView `json:"command"`
	Gate    GateView    `json:"gate"`
}

type ClearGateFaultResponse struct {
	Gate      GateView              `json:"gate"`
	Clearance ClearanceEvidenceView `json:"clearance"`
}
