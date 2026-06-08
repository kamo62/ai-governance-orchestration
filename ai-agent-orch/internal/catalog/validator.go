package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var forbiddenMarkdownTokens = []string{
	"tools_allowed:",
	"permissions:",
	"governance:",
	"cost:",
	"runtime:",
	"model_id:",
}

type Report struct {
	Agents       []AgentSummary `json:"agents"`
	ModelAliases []string       `json:"model_aliases"`
}

type AgentSummary struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	WorkspaceMode string `json:"workspace_write"`
}

func (r Report) HasAgent(name string) bool {
	for _, agent := range r.Agents {
		if agent.Name == name {
			return true
		}
	}
	return false
}

func Validate(root string) (Report, error) {
	aliases, err := loadModelAliases(filepath.Join(root, "models", "registry.yaml"))
	if err != nil {
		return Report{}, err
	}
	mcpRegistrations, err := loadMCPRegistrations(root)
	if err != nil {
		return Report{}, err
	}

	agentDirs, err := discoverAgentDirs(filepath.Join(root, "agents"))
	if err != nil {
		return Report{}, err
	}

	var report Report
	for alias := range aliases {
		report.ModelAliases = append(report.ModelAliases, alias)
	}
	sort.Strings(report.ModelAliases)

	var tempAgents, publishedAgents []string
	seenAgents := make(map[string]string)
	for _, dir := range agentDirs {
		cfg, err := validateAgentDir(root, dir, aliases, mcpRegistrations)
		if err != nil {
			return Report{}, err
		}
		relPath := filepath.ToSlash(rel(root, dir))
		if firstPath, exists := seenAgents[cfg.Name]; exists {
			return Report{}, fmt.Errorf("duplicate agent name %q in %s and %s", cfg.Name, firstPath, relPath)
		}
		seenAgents[cfg.Name] = relPath
		report.Agents = append(report.Agents, AgentSummary{
			Name:          cfg.Name,
			Path:          relPath,
			WorkspaceMode: cfg.Permissions.WorkspaceWrite,
		})
		if strings.Contains(relPath, "agents/temp/") {
			tempAgents = append(tempAgents, cfg.Name)
		}
		if strings.Contains(relPath, "agents/published/") {
			publishedAgents = append(publishedAgents, cfg.Name)
		}
	}

	sort.Slice(report.Agents, func(i, j int) bool { return report.Agents[i].Name < report.Agents[j].Name })
	sort.Strings(tempAgents)
	sort.Strings(publishedAgents)

	if err := validateRouterCoverage(root, "temp", tempAgents); err != nil {
		return Report{}, err
	}
	if err := validateRouterCoverage(root, "published", publishedAgents); err != nil {
		return Report{}, err
	}

	return report, nil
}

func loadModelAliases(path string) (map[string]struct{}, error) {
	var registry ModelRegistry
	if err := readYAML(path, &registry); err != nil {
		return nil, fmt.Errorf("load model registry: %w", err)
	}
	if len(registry.Models) == 0 {
		return nil, errors.New("model registry has no models")
	}
	aliases := make(map[string]struct{}, len(registry.Models))
	for _, model := range registry.Models {
		if model.Alias == "" {
			return nil, errors.New("model registry contains model without alias")
		}
		aliases[model.Alias] = struct{}{}
	}
	return aliases, nil
}

func discoverAgentDirs(root string) ([]string, error) {
	var dirs []string
	for _, group := range []string{"core", "temp", "published"} {
		matches, err := filepath.Glob(filepath.Join(root, group, "*"))
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				return nil, err
			}
			if info.IsDir() {
				dirs = append(dirs, match)
			}
		}
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no agent directories under %s", root)
	}
	sort.Strings(dirs)
	return dirs, nil
}

func validateAgentDir(root string, dir string, aliases map[string]struct{}, mcpRegistrations map[string]MCPRegistration) (AgentConfig, error) {
	mdPath := filepath.Join(dir, "agent.md")
	cfgPath := filepath.Join(dir, "agent.config.yaml")
	evalsPath := filepath.Join(dir, "evals", "golden-cases.yaml")

	for _, path := range []string{mdPath, cfgPath, evalsPath} {
		if _, err := os.Stat(path); err != nil {
			return AgentConfig{}, fmt.Errorf("%s: required file missing: %w", rel(root, path), err)
		}
	}

	md, err := os.ReadFile(mdPath)
	if err != nil {
		return AgentConfig{}, err
	}
	mdText := string(md)
	if !strings.Contains(mdText, "Config: `./agent.config.yaml`") {
		return AgentConfig{}, fmt.Errorf("%s: missing Config reference", rel(root, mdPath))
	}
	for _, token := range forbiddenMarkdownTokens {
		if strings.Contains(mdText, token) {
			return AgentConfig{}, fmt.Errorf("%s: contains executable policy token %q", rel(root, mdPath), token)
		}
	}

	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		return AgentConfig{}, err
	}
	if strings.Contains(string(cfgBytes), "model_id:") {
		return AgentConfig{}, fmt.Errorf("%s: concrete model_id is only allowed in models/registry.yaml", rel(root, cfgPath))
	}

	var cfg AgentConfig
	if err := yaml.Unmarshal(cfgBytes, &cfg); err != nil {
		return AgentConfig{}, fmt.Errorf("%s: parse config: %w", rel(root, cfgPath), err)
	}
	if err := cfg.validate(aliases); err != nil {
		return AgentConfig{}, fmt.Errorf("%s: %w", rel(root, cfgPath), err)
	}
	if err := validateMCPReferences(cfg, mcpRegistrations); err != nil {
		return AgentConfig{}, fmt.Errorf("%s: %w", rel(root, cfgPath), err)
	}

	// Combine legacy tools_allowed and new governance_profile.mcp_tools for validation.
	allTools := append([]string{}, cfg.ToolsAllowed...)
	allTools = append(allTools, cfg.GovernanceProfile.MCPTools...)
	if cfg.Permissions.WorkspaceWrite == "deny" && contains(allTools, "write_file") {
		return AgentConfig{}, fmt.Errorf("%s: read-only agent cannot allow write_file", rel(root, cfgPath))
	}
	if cfg.Name != "unit-tests" && contains(allTools, "run_command:playwright") {
		return AgentConfig{}, fmt.Errorf("%s: only unit-tests can run Playwright", rel(root, cfgPath))
	}

	// Stricter validation for published agents.
	if strings.Contains(filepath.ToSlash(rel(root, dir)), "agents/published/") {
		if err := validatePublishedAgent(cfg); err != nil {
			return AgentConfig{}, fmt.Errorf("%s: %w", rel(root, cfgPath), err)
		}
	}

	return cfg, nil
}

type MCPRegistration struct {
	ServerID      string        `yaml:"server_id"`
	Endpoint      string        `yaml:"endpoint"`
	AuthMode      string        `yaml:"auth_mode"`
	AllowedAgents []string      `yaml:"allowed_agents"`
	ToolPolicy    MCPToolPolicy `yaml:"tool_policy"`
	DataResidency string        `yaml:"data_residency"`
}

type MCPToolPolicy struct {
	DefaultApproval string   `yaml:"default_approval"`
	Allow           []string `yaml:"allow"`
	Deny            []string `yaml:"deny"`
}

func loadMCPRegistrations(root string) (map[string]MCPRegistration, error) {
	return LoadMCPRegistrations(root)
}

func LoadMCPRegistrations(root string) (map[string]MCPRegistration, error) {
	registrationDir := filepath.Join(root, "mcp", "registrations")
	matches, err := filepath.Glob(filepath.Join(registrationDir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no MCP registrations under %s", rel(root, registrationDir))
	}
	registrations := make(map[string]MCPRegistration, len(matches))
	for _, path := range matches {
		var reg MCPRegistration
		if err := readYAML(path, &reg); err != nil {
			return nil, fmt.Errorf("load MCP registration %s: %w", rel(root, path), err)
		}
		if reg.ServerID == "" {
			return nil, fmt.Errorf("%s: missing server_id", rel(root, path))
		}
		if _, exists := registrations[reg.ServerID]; exists {
			return nil, fmt.Errorf("duplicate MCP registration %q", reg.ServerID)
		}
		registrations[reg.ServerID] = reg
	}
	return registrations, nil
}

func validateMCPReferences(cfg AgentConfig, registrations map[string]MCPRegistration) error {
	for _, serverID := range cfg.MCPServers {
		reg, ok := registrations[serverID]
		if !ok {
			return fmt.Errorf("unknown mcp server %q", serverID)
		}
		if len(reg.AllowedAgents) > 0 && !contains(reg.AllowedAgents, cfg.Name) {
			return fmt.Errorf("mcp server %q does not allow agent %q", serverID, cfg.Name)
		}
	}
	return nil
}

func validatePublishedAgent(cfg AgentConfig) error {
	if cfg.Phase != "published" {
		return fmt.Errorf("published agent must have phase: published, got %q", cfg.Phase)
	}
	if !cfg.Evals.RequiredForPhase0 {
		return errors.New("published agent must have evals.required_for_phase0: true")
	}
	if cfg.Version == "" || cfg.Version[0] == '0' {
		return fmt.Errorf("published agent version must be >= 1.0.0, got %q", cfg.Version)
	}
	return nil
}

func validateRouterCoverage(root string, group string, agents []string) error {
	if len(agents) == 0 {
		return nil
	}
	var evals struct {
		Cases []struct {
			ExpectedSpecialist string `yaml:"expected_specialist"`
		} `yaml:"cases"`
	}
	path := filepath.Join(root, "agents", "core", "router-agent", "evals", "golden-cases.yaml")
	if err := readYAML(path, &evals); err != nil {
		return fmt.Errorf("load router evals: %w", err)
	}
	covered := map[string]struct{}{}
	for _, c := range evals.Cases {
		if c.ExpectedSpecialist != "" {
			covered[c.ExpectedSpecialist] = struct{}{}
		}
	}
	for _, agent := range agents {
		if _, ok := covered[agent]; !ok {
			return fmt.Errorf("router golden cases do not cover %s agent %q", group, agent)
		}
	}
	return nil
}

// SystemPrompt reads the agent's prose instructions from agent.md.
func (cfg AgentConfig) SystemPrompt(root string) string {
	// Search published first, then temp, then core.
	for _, group := range []string{"published", "temp", "core"} {
		path := filepath.Join(root, "agents", group, cfg.Name, "agent.md")
		if _, err := os.Stat(path); err == nil {
			b, err := os.ReadFile(path)
			if err == nil {
				return string(b)
			}
		}
	}
	return ""
}

// LoadAgentConfig loads an agent configuration by name from the catalog.
func LoadAgentConfig(root, name string) (AgentConfig, error) {
	aliases, err := loadModelAliases(filepath.Join(root, "models", "registry.yaml"))
	if err != nil {
		return AgentConfig{}, err
	}
	mcpRegistrations, err := loadMCPRegistrations(root)
	if err != nil {
		return AgentConfig{}, err
	}

	// Try published first, then temp, then core.
	for _, group := range []string{"published", "temp", "core"} {
		dir := filepath.Join(root, "agents", group, name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return validateAgentDir(root, dir, aliases, mcpRegistrations)
		}
	}

	// Try router-agent specifically.
	if name == "router-agent" {
		dir := filepath.Join(root, "agents", "core", "router-agent")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return validateAgentDir(root, dir, aliases, mcpRegistrations)
		}
	}

	return AgentConfig{}, fmt.Errorf("agent %q not found", name)
}

func readYAML(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, out)
}

func rel(root, path string) string {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// AgentConfig is the exported agent configuration loaded from agent.config.yaml.
// It describes a governance profile, not a native tool allow-list.
type AgentConfig struct {
	Name         string   `yaml:"name"`
	Version      string   `yaml:"version"`
	Phase        string   `yaml:"phase"`
	Owner        string   `yaml:"owner"`
	Runtime      string   `yaml:"runtime"`
	Model        modelRef `yaml:"model"`
	MCPServers   []string `yaml:"mcp_servers"`
	ToolsAllowed []string `yaml:"tools_allowed"` // Deprecated: use GovernanceProfile.MCPTools
	Permissions  struct {
		Network               string `yaml:"network"`
		WorkspaceWrite        string `yaml:"workspace_write"`
		OutsideWorkspaceWrite string `yaml:"outside_workspace_write"`
	} `yaml:"permissions"`
	Governance struct {
		ClassificationMax string `yaml:"classification_max"`
	} `yaml:"governance"`
	Cost struct {
		PerInvocationCapUSD    float64 `yaml:"per_invocation_cap_usd"`
		ConsecutiveToolCallMax int     `yaml:"consecutive_tool_call_max"`
	} `yaml:"cost"`
	Evals struct {
		Path              string `yaml:"path"`
		RequiredForPhase0 bool   `yaml:"required_for_phase0"`
	} `yaml:"evals"`
	GovernanceProfile struct {
		RoutingHints          []string `yaml:"routing_hints"`
		ModelPolicy           string   `yaml:"model_policy"`
		EvidenceExpectations  []string `yaml:"evidence_expectations"`
		MCPTools              []string `yaml:"mcp_tools"`
		ReportingRequirements []string `yaml:"reporting_requirements"`
	} `yaml:"governance_profile"`
}

type modelRef struct {
	Primary  string `yaml:"primary"`
	Fallback string `yaml:"fallback"`
}

func (cfg AgentConfig) validate(aliases map[string]struct{}) error {
	required := map[string]string{
		"name":                          cfg.Name,
		"version":                       cfg.Version,
		"phase":                         cfg.Phase,
		"owner":                         cfg.Owner,
		"runtime":                       cfg.Runtime,
		"model.primary":                 cfg.Model.Primary,
		"permissions.network":           cfg.Permissions.Network,
		"permissions.workspace_write":   cfg.Permissions.WorkspaceWrite,
		"governance.classification_max": cfg.Governance.ClassificationMax,
		"evals.path":                    cfg.Evals.Path,
	}
	for field, value := range required {
		if value == "" {
			return fmt.Errorf("missing required field %s", field)
		}
	}
	if len(cfg.MCPServers) == 0 {
		return errors.New("missing mcp_servers")
	}
	if cfg.Cost.PerInvocationCapUSD <= 0 {
		return errors.New("cost.per_invocation_cap_usd must be positive")
	}
	if cfg.Cost.ConsecutiveToolCallMax <= 0 {
		return errors.New("cost.consecutive_tool_call_max must be positive")
	}
	if _, ok := aliases[cfg.Model.Primary]; !ok {
		return fmt.Errorf("unknown primary model alias %q", cfg.Model.Primary)
	}
	if cfg.Model.Fallback != "" {
		if _, ok := aliases[cfg.Model.Fallback]; !ok {
			return fmt.Errorf("unknown fallback model alias %q", cfg.Model.Fallback)
		}
	}
	return nil
}
