package composition

import (
	"errors"
	"fmt"
	"sync"
)

// ErrHumanGateRequired is returned when a stage requires explicit user approval before proceeding.
var ErrHumanGateRequired = errors.New("human gate required before next stage")

// ErrMaxDepthExceeded is returned when composition exceeds the maximum allowed depth.
var ErrMaxDepthExceeded = errors.New("composition max depth exceeded")

// Stage represents one step in a sequential composition.
type Stage struct {
	Name        string            `json:"name" yaml:"name"`
	Agent       string            `json:"agent" yaml:"agent"`
	ModelAlias  string            `json:"model_alias,omitempty" yaml:"model_alias,omitempty"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Input       string            `json:"input,omitempty" yaml:"input,omitempty"` // governed context from prior stage
	Output      string            `json:"output,omitempty" yaml:"output,omitempty"`
	Approved    bool              `json:"approved" yaml:"approved"`
	Completed   bool              `json:"completed" yaml:"completed"`
	Metadata    map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// Composition is an ordered sequence of stages with a human gate between each.
type Composition struct {
	SessionID   string  `json:"session_id" yaml:"session_id"`
	Stages      []Stage `json:"stages" yaml:"stages"`
	CurrentIdx  int     `json:"current_idx" yaml:"current_idx"`
	MaxDepth    int     `json:"max_depth" yaml:"max_depth"`
	Description string  `json:"description,omitempty" yaml:"description,omitempty"`
}

// Clone returns an independent copy that callers may inspect without holding
// the store lock.
func (c *Composition) Clone() *Composition {
	if c == nil {
		return nil
	}
	clone := *c
	clone.Stages = cloneStages(c.Stages)
	return &clone
}

// NewComposition creates a composition with the default max depth of 2.
func NewComposition(sessionID string, stages []Stage) *Composition {
	return &Composition{
		SessionID:  sessionID,
		Stages:     cloneStages(stages),
		CurrentIdx: 0,
		MaxDepth:   2,
	}
}

// CanProceed checks whether the composition can advance to the next stage.
func (c *Composition) CanProceed() error {
	if c == nil {
		return errors.New("composition is nil")
	}
	if c.CurrentIdx >= len(c.Stages) {
		return errors.New("all stages completed")
	}
	if !c.Stages[c.CurrentIdx].Completed {
		return errors.New("current stage not completed")
	}
	if !c.Stages[c.CurrentIdx].Approved {
		return ErrHumanGateRequired
	}
	if c.CurrentIdx+1 >= c.MaxDepth {
		return ErrMaxDepthExceeded
	}
	return nil
}

// ApproveStage marks the current stage as approved by the user, allowing the next stage to proceed.
func (c *Composition) ApproveStage() error {
	if c == nil {
		return errors.New("composition is nil")
	}
	if c.CurrentIdx >= len(c.Stages) {
		return errors.New("no stage to approve")
	}
	c.Stages[c.CurrentIdx].Approved = true
	return nil
}

// CompleteStage records the output of the current stage and marks it completed.
func (c *Composition) CompleteStage(output string) error {
	if c == nil {
		return errors.New("composition is nil")
	}
	if c.CurrentIdx >= len(c.Stages) {
		return errors.New("no stage to complete")
	}
	c.Stages[c.CurrentIdx].Output = output
	c.Stages[c.CurrentIdx].Completed = true
	return nil
}

// Advance moves to the next stage, carrying the prior stage's output as governed context.
func (c *Composition) Advance() error {
	if err := c.CanProceed(); err != nil {
		return err
	}
	c.CurrentIdx++
	if c.CurrentIdx < len(c.Stages) {
		// Carry forward output from previous stage as input context.
		c.Stages[c.CurrentIdx].Input = c.Stages[c.CurrentIdx-1].Output
	}
	return nil
}

// IsComplete reports whether all stages are completed.
func (c *Composition) IsComplete() bool {
	if c == nil {
		return false
	}
	return c.CurrentIdx >= len(c.Stages)
}

// CurrentStage returns the active stage, if any.
func (c *Composition) CurrentStage() (Stage, error) {
	if c == nil {
		return Stage{}, errors.New("composition is nil")
	}
	if c.CurrentIdx >= len(c.Stages) {
		return Stage{}, errors.New("no active stage")
	}
	return c.Stages[c.CurrentIdx], nil
}

// CompositionStore tracks in-flight compositions by session ID.
type CompositionStore struct {
	mu           sync.RWMutex
	compositions map[string]*Composition
}

func NewCompositionStore() *CompositionStore {
	return &CompositionStore{compositions: make(map[string]*Composition)}
}

func (s *CompositionStore) Get(sessionID string) (*Composition, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.compositions[sessionID]
	if !ok {
		return nil, false
	}
	return c.Clone(), true
}

func (s *CompositionStore) Update(sessionID string, mutate func(*Composition) error) (*Composition, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.compositions[sessionID]
	if !ok {
		return nil, false, nil
	}
	if err := mutate(c); err != nil {
		return nil, true, err
	}
	return c.Clone(), true, nil
}

func (s *CompositionStore) Exists(sessionID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.compositions[sessionID]
	return ok
}

func (s *CompositionStore) Set(sessionID string, c *Composition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c == nil {
		delete(s.compositions, sessionID)
		return
	}
	s.compositions[sessionID] = c.Clone()
}

func (s *CompositionStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.compositions, sessionID)
}

// ValidateStages checks that all stages reference real agents and that the depth is within bounds.
func ValidateStages(stages []Stage, maxDepth int) error {
	if len(stages) == 0 {
		return errors.New("composition must have at least one stage")
	}
	if len(stages) > maxDepth {
		return fmt.Errorf("composition has %d stages, max depth is %d", len(stages), maxDepth)
	}
	for i, stage := range stages {
		if stage.Agent == "" {
			return fmt.Errorf("stage %d: agent is required", i)
		}
		if stage.Name == "" {
			return fmt.Errorf("stage %d: name is required", i)
		}
	}
	return nil
}

func cloneStages(stages []Stage) []Stage {
	if len(stages) == 0 {
		return nil
	}
	clone := make([]Stage, len(stages))
	for i, stage := range stages {
		clone[i] = stage
		if len(stage.Metadata) > 0 {
			clone[i].Metadata = make(map[string]string, len(stage.Metadata))
			for key, value := range stage.Metadata {
				clone[i].Metadata[key] = value
			}
		}
	}
	return clone
}
