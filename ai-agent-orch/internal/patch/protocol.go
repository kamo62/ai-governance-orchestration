package patch

import (
	"encoding/json"
	"strconv"
	"strings"
)

// PatchEnvelope is the wire format for proposed file changes.
type PatchEnvelope struct {
	ProtocolVersion int         `json:"protocolVersion"`
	PatchID         string      `json:"patchId"`
	BufferID        string      `json:"bufferId,omitempty"`
	SessionID       string      `json:"sessionId"`
	Summary         string      `json:"summary"`
	Rationale       string      `json:"rationale"`
	Files           []PatchFile `json:"files"`
}

func (p *PatchEnvelope) UnmarshalJSON(data []byte) error {
	type alias PatchEnvelope
	var raw struct {
		ProtocolVersion json.RawMessage `json:"protocolVersion"`
		*alias
	}
	raw.alias = (*alias)(p)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.ProtocolVersion) == 0 {
		return nil
	}
	var version int
	if err := json.Unmarshal(raw.ProtocolVersion, &version); err == nil {
		p.ProtocolVersion = version
		return nil
	}
	var versionText string
	if err := json.Unmarshal(raw.ProtocolVersion, &versionText); err == nil {
		parsed, err := strconv.Atoi(strings.TrimSpace(versionText))
		if err == nil {
			p.ProtocolVersion = parsed
		}
		return nil
	}
	return nil
}

// PatchFile describes one file change.
type PatchFile struct {
	Path                string `json:"path"`
	Action              string `json:"action"` // create | modify | delete
	OriginalContentHash string `json:"originalContentHash,omitempty"`
	ProposedContentHash string `json:"proposedContentHash,omitempty"`
	OriginalContent     string `json:"originalContent,omitempty"` // transient only
	NewContent          string `json:"newContent,omitempty"`      // transient only
}

// PatchDecision records the user's choice.
type PatchDecision struct {
	PatchID  string `json:"patch_id"`
	Decision string `json:"decision"` // applied | partially_applied | rejected
	Reason   string `json:"reason,omitempty"`
}
