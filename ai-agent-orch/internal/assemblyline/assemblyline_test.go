package assemblyline

import (
	"testing"
)

func TestParseYAML(t *testing.T) {
	data := []byte(`
name: bug-investigation
version: "1.0.0"
max_depth: 2
stages:
  - name: investigate
    agent: security-scan
    human_gate: true
  - name: plan
    agent: architecture-review
    human_gate: true
`)
	line, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	if line.Name != "bug-investigation" {
		t.Fatalf("expected name bug-investigation, got %q", line.Name)
	}
	if len(line.Stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(line.Stages))
	}
	if line.Stages[0].Agent != "security-scan" {
		t.Fatalf("expected agent security-scan, got %q", line.Stages[0].Agent)
	}
}

func TestAssemblyLineValidate(t *testing.T) {
	// Missing name.
	if err := (&AssemblyLine{Version: "1.0", Stages: []LineStage{{Name: "a", Agent: "x"}}}).Validate(); err == nil {
		t.Fatal("expected error for missing name")
	}
	// Missing version.
	if err := (&AssemblyLine{Name: "x", Stages: []LineStage{{Name: "a", Agent: "x"}}}).Validate(); err == nil {
		t.Fatal("expected error for missing version")
	}
	// Too many stages.
	if err := (&AssemblyLine{Name: "x", Version: "1", MaxDepth: 1, Stages: []LineStage{{Name: "a", Agent: "x"}, {Name: "b", Agent: "y"}}}).Validate(); err == nil {
		t.Fatal("expected error for too many stages")
	}
	// Missing agent.
	if err := (&AssemblyLine{Name: "x", Version: "1", Stages: []LineStage{{Name: "a"}}}).Validate(); err == nil {
		t.Fatal("expected error for missing agent")
	}
}

func TestAssemblyLineStageNames(t *testing.T) {
	line := &AssemblyLine{
		Stages: []LineStage{
			{Name: "investigate"},
			{Name: "plan"},
			{Name: "execute"},
		},
	}
	names := line.StageNames()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	if names[0] != "investigate" || names[1] != "plan" || names[2] != "execute" {
		t.Fatalf("unexpected names: %v", names)
	}
}
