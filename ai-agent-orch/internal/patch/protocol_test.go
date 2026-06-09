package patch

import (
	"encoding/json"
	"testing"
)

func TestPatchEnvelopeRoundTrip(t *testing.T) {
	in := PatchEnvelope{
		ProtocolVersion: 1,
		PatchID:         "patch_123",
		SessionID:       "sess_123",
		Summary:         "add smoke file",
		Rationale:       "beta verification",
		Files: []PatchFile{
			{
				Path:       "SMOKE.md",
				Action:     "create",
				NewContent: "# smoke\n",
			},
		},
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out PatchEnvelope
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.PatchID != in.PatchID || out.SessionID != in.SessionID || len(out.Files) != 1 {
		t.Fatalf("unexpected round-trip: %+v", out)
	}
	if out.Files[0].Path != "SMOKE.md" || out.Files[0].Action != "create" {
		t.Fatalf("unexpected file: %+v", out.Files[0])
	}
}

func TestPatchDecisionJSON(t *testing.T) {
	in := PatchDecision{
		PatchID:  "patch_123",
		Decision: "applied",
		Reason:   "beta smoke",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatal("expected valid JSON")
	}
}

func TestPatchEnvelopeAcceptsStringProtocolVersion(t *testing.T) {
	var out PatchEnvelope
	if err := json.Unmarshal([]byte(`{"protocolVersion":"1","patchId":"patch_123","sessionId":"sess_123","files":[{"path":"AI_ORCH_REVIEW_FINDINGS.md","action":"create","newContent":"findings"}]}`), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ProtocolVersion != 1 {
		t.Fatalf("expected protocol version 1, got %d", out.ProtocolVersion)
	}
}
