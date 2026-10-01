package domain

import (
	"strings"
	"time"
)

const maxClearanceNoteLength = 1000

// NewClearanceEvidence validates the handling basis submitted for a fault
// clearance. The maintainer identity and a handling reference are mandatory;
// the note is optional context recorded alongside the reference.
func NewClearanceEvidence(handledBy, reference, note string, now time.Time) (ClearanceEvidence, error) {
	handledBy = strings.TrimSpace(handledBy)
	if handledBy == "" {
		return ClearanceEvidence{}, Invalid("invalid_handled_by", "handled_by is required")
	}

	reference = strings.TrimSpace(reference)
	if reference == "" {
		return ClearanceEvidence{}, Invalid("invalid_clearance_reference", "handling reference is required as clearance evidence")
	}

	note = strings.TrimSpace(note)
	if len(note) > maxClearanceNoteLength {
		return ClearanceEvidence{}, Invalid("invalid_clearance_note", "clearance note is too long")
	}

	return ClearanceEvidence{
		HandledBy: handledBy,
		Reference: reference,
		Note:      note,
		ClearedAt: now.UTC(),
	}, nil
}
