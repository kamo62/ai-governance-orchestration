package dispatch

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// CommandAllowlist is the parsed command-allowlists policy.
type CommandAllowlist struct {
	SystemCommands []SystemCommand `yaml:"system_commands"`
}

// SystemCommand defines a single command and its allow rules.
type SystemCommand struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Default     string       `yaml:"default"`
	Requires    string       `yaml:"requires,omitempty"`
	Subcommands []Subcommand `yaml:"subcommands,omitempty"`
}

// Subcommand defines a subcommand and which agents may use it.
type Subcommand struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	AllowedAgents []string `yaml:"allowed_agents"`
}

// LoadCommandAllowlist loads the command allowlist from the given path.
func LoadCommandAllowlist(path string) (*CommandAllowlist, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read command allowlist: %w", err)
	}
	var list CommandAllowlist
	if err := yaml.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("parse command allowlist: %w", err)
	}
	return &list, nil
}

// IsAllowed reports whether the given command (and optional subcommand) is
// allowed for the specified agent according to the allowlist.
func (list *CommandAllowlist) IsAllowed(command, subcommand, agent string) bool {
	return list.IsAllowedWithPermissions(command, subcommand, agent, nil)
}

// IsAllowedWithPermissions reports whether a command is allowed for the agent
// and the effective permission attributes declared in agent.config.yaml.
func (list *CommandAllowlist) IsAllowedWithPermissions(command, subcommand, agent string, permissions map[string]string) bool {
	if list == nil {
		return false // fail-closed when no policy is loaded
	}
	for _, cmd := range list.SystemCommands {
		if cmd.Name != command {
			continue
		}
		if cmd.Default == "deny" {
			// Check subcommands for explicit allow.
			for _, sub := range cmd.Subcommands {
				if sub.Name == subcommand {
					for _, a := range sub.AllowedAgents {
						if a == agent {
							return true
						}
					}
					return false
				}
			}
			if subcommand == "" && requirementSatisfied(cmd.Requires, permissions) {
				return true
			}
			return false
		}
		if cmd.Default == "allow" {
			return true
		}
	}
	return false // unknown command: deny
}

func requirementSatisfied(requirement string, permissions map[string]string) bool {
	if requirement == "" {
		return false
	}
	parts := strings.Split(requirement, "=")
	if len(parts) != 2 {
		return false
	}
	key := strings.TrimSpace(parts[0])
	want := strings.TrimSpace(parts[1])
	if key == "" || want == "" {
		return false
	}
	return permissions[key] == want
}

// ToolBroker validates runtime tool calls against the command allowlist.
type ToolBroker struct {
	allowlist *CommandAllowlist
}

// NewToolBroker creates a broker from the given policy path.
func NewToolBroker(path string) (*ToolBroker, error) {
	list, err := LoadCommandAllowlist(path)
	if err != nil {
		return nil, err
	}
	return &ToolBroker{allowlist: list}, nil
}

// Validate checks whether a tool invocation is permitted.
// The command should be the tool name (e.g. "run_command").
// The subcommand should be the specific subcommand (e.g. "playwright").
// The agent is the requesting specialist name.
func (b *ToolBroker) Validate(command, subcommand, agent string) error {
	return b.ValidateWithPermissions(command, subcommand, agent, nil)
}

// ValidateWithPermissions checks whether a tool invocation is permitted using
// both command allow-list rules and effective agent permissions.
func (b *ToolBroker) ValidateWithPermissions(command, subcommand, agent string, permissions map[string]string) error {
	if b == nil || b.allowlist == nil {
		return fmt.Errorf("tool broker not configured")
	}
	if !b.allowlist.IsAllowedWithPermissions(command, subcommand, agent, permissions) {
		reason := fmt.Sprintf("command %q", command)
		if subcommand != "" {
			reason = fmt.Sprintf("command %q subcommand %q", command, subcommand)
		}
		return fmt.Errorf("%s is not allowed for agent %q", reason, agent)
	}
	return nil
}

// ParseToolCommand splits a tool identifier like "run_command:playwright"
// into command and subcommand.
func ParseToolCommand(tool string) (command, subcommand string) {
	parts := strings.SplitN(tool, ":", 2)
	command = parts[0]
	if len(parts) == 2 {
		subcommand = parts[1]
	}
	return
}
