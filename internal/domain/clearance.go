package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// maintainerPrefix is the authorization seam for on-site operations staff.
// Gate fault clearance is restricted to maintainer identities (ops-*);
// dispatchers (dispatcher-*), verifiers (safety-*) and operators do not match.
const maintainerPrefix = "ops-"

const minEvidenceRunes = 4

// FaultRecord captures the latched execution fault that put a gate into the
// faulted status. It is the explicit fault fact a clearance must reference.
type FaultRecord struct {
	CommandID  string
	Reason     string
	Aperture   float64
	OccurredAt time.Time
}

// ClearanceRecord captures one formal fault-clearance action and the handling
// basis the maintainer submitted. A record exists only for a successful
// faulted -> idle transition.
type ClearanceRecord struct {
	FaultCommandID string
	Handler        string
	Evidence       string
	Note           string
	ClearedAt      time.Time
}

// NewClearanceRecord validates the handling basis a maintainer submits for a
// latched fault. The faulting command id is required so the clearance can be
// cross-checked against the explicit fault fact and the aborted command.
func NewClearanceRecord(faultCommandID, handler, evidence, note string, now time.Time) (ClearanceRecord, error) {
	faultCommandID = strings.TrimSpace(faultCommandID)
	if faultCommandID == "" {
		return ClearanceRecord{}, Invalid("invalid_fault_command", "command_id of the faulting command is required")
	}

	handler = strings.TrimSpace(handler)
	if handler == "" {
		return ClearanceRecord{}, Invalid("invalid_handler", "handler is required")
	}
	if !strings.HasPrefix(handler, maintainerPrefix) {
		return ClearanceRecord{}, Forbidden(
			"clearance_not_authorized",
			"only authorized maintainers (ops-* identities) can clear a latched fault",
		)
	}

	evidence = strings.TrimSpace(evidence)
	if utf8.RuneCountInString(evidence) < minEvidenceRunes {
		return ClearanceRecord{}, Invalid(
			"insufficient_clearance_evidence",
			"handling basis must describe the on-site disposition",
		)
	}

	return ClearanceRecord{
		FaultCommandID: faultCommandID,
		Handler:        handler,
		Evidence:       evidence,
		Note:           strings.TrimSpace(note),
		ClearedAt:      now.UTC(),
	}, nil
}
