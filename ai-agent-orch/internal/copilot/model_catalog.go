package copilot

import (
	"encoding/json"
	"strings"
)

// PickerModel is a Copilot model that should be visible in editor model
// pickers. Alias is the governed ai-orch alias; ModelID is the upstream
// Copilot model id used when forwarding actor-bound requests.
type PickerModel struct {
	Alias       string
	ModelID     string
	DisplayName string
	Family      string
	Type        string
	Preview     bool
}

// PickerChatModelsFromCatalog extracts dynamic chat models that Copilot exposes
// to the authenticated actor's picker. Hidden internal entries and non-chat
// models are deliberately excluded from governed editor config.
func PickerChatModelsFromCatalog(body []byte) ([]PickerModel, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(root["data"], &entries); err != nil {
		return nil, err
	}

	models := make([]PickerModel, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(catalogString(entry["id"]))
		if id == "" || !catalogBool(entry["model_picker_enabled"]) {
			continue
		}
		capabilities := catalogObject(entry["capabilities"])
		modelType := strings.TrimSpace(catalogString(capabilities["type"]))
		if modelType != "" && !strings.EqualFold(modelType, "chat") {
			continue
		}
		alias := GovernedModelAlias(id)
		if alias == "copilot-" {
			continue
		}
		displayName := strings.TrimSpace(catalogString(entry["name"]))
		if displayName == "" {
			displayName = id
		}
		models = append(models, PickerModel{
			Alias:       alias,
			ModelID:     id,
			DisplayName: displayName,
			Family:      strings.TrimSpace(catalogString(capabilities["family"])),
			Type:        modelType,
			Preview:     catalogBool(entry["preview"]),
		})
	}
	return models, nil
}

// FindPickerChatModelByAlias resolves a governed Copilot picker alias back to
// the upstream Copilot model id for the current actor.
func FindPickerChatModelByAlias(body []byte, alias string) (PickerModel, bool, error) {
	models, err := PickerChatModelsFromCatalog(body)
	if err != nil {
		return PickerModel{}, false, err
	}
	alias = strings.TrimSpace(alias)
	for _, model := range models {
		if strings.EqualFold(model.Alias, alias) {
			return model, true, nil
		}
	}
	return PickerModel{}, false, nil
}

func GovernedModelAlias(modelID string) string {
	return "copilot-" + safeModelIDAlias(modelID)
}

func safeModelIDAlias(modelID string) string {
	modelID = strings.TrimSpace(strings.ToLower(modelID))
	var b strings.Builder
	lastDash := false
	for _, r := range modelID {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func catalogString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return ""
}

func catalogBool(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return false
}

func catalogObject(raw json.RawMessage) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	return obj
}
