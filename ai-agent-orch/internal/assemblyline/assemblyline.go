package assemblyline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// AssemblyLine is a YAML-defined ordered pipeline of agent stages.
// It does not replace the agent catalog; it is an evaluation and
// reference format for multi-stage workflows.
type AssemblyLine struct {
	Name        string      `json:"name" yaml:"name"`
	Description string      `json:"description,omitempty" yaml:"description,omitempty"`
	Version     string      `json:"version" yaml:"version"`
	MaxDepth    int         `json:"max_depth" yaml:"max_depth"`
	Stages      []LineStage `json:"stages" yaml:"stages"`
}

// LineStage represents one stage in an assembly line.
type LineStage struct {
	Name        string            `json:"name" yaml:"name"`
	Agent       string            `json:"agent" yaml:"agent"`
	ModelAlias  string            `json:"model_alias,omitempty" yaml:"model_alias,omitempty"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	ContextIn   string            `json:"context_in,omitempty" yaml:"context_in,omitempty"`
	ContextOut  string            `json:"context_out,omitempty" yaml:"context_out,omitempty"`
	HumanGate   bool              `json:"human_gate" yaml:"human_gate"`
	Metadata    map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// ParseYAML reads an assembly line definition from YAML bytes.
func ParseYAML(data []byte) (*AssemblyLine, error) {
	var line AssemblyLine
	if err := yaml.Unmarshal(data, &line); err != nil {
		return nil, fmt.Errorf("parse assembly line yaml: %w", err)
	}
	if err := line.Validate(); err != nil {
		return nil, err
	}
	return &line, nil
}

// ParseYAMLFile reads an assembly line definition from a YAML file.
func ParseYAMLFile(path string) (*AssemblyLine, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read assembly line file: %w", err)
	}
	return ParseYAML(data)
}

// Validate checks that the assembly line is well-formed.
func (a *AssemblyLine) Validate() error {
	if a == nil {
		return errors.New("assembly line is nil")
	}
	if a.Name == "" {
		return errors.New("assembly line name is required")
	}
	if a.Version == "" {
		return errors.New("assembly line version is required")
	}
	if len(a.Stages) == 0 {
		return errors.New("assembly line must have at least one stage")
	}
	maxDepth := a.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}
	if len(a.Stages) > maxDepth {
		return fmt.Errorf("assembly line has %d stages, max depth is %d", len(a.Stages), maxDepth)
	}
	for i, stage := range a.Stages {
		if stage.Name == "" {
			return fmt.Errorf("stage %d: name is required", i)
		}
		if stage.Agent == "" {
			return fmt.Errorf("stage %d: agent is required", i)
		}
	}
	return nil
}

// ToJSON returns the assembly line as JSON bytes.
func (a *AssemblyLine) ToJSON() ([]byte, error) {
	if a == nil {
		return nil, errors.New("assembly line is nil")
	}
	return json.MarshalIndent(a, "", "  ")
}

// StageNames returns the ordered list of stage names.
func (a *AssemblyLine) StageNames() []string {
	if a == nil {
		return nil
	}
	names := make([]string, len(a.Stages))
	for i, s := range a.Stages {
		names[i] = s.Name
	}
	return names
}
