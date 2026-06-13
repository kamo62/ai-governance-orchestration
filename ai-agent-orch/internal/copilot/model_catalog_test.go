package copilot

import "testing"

func TestPickerChatModelAliasesFromCatalog(t *testing.T) {
	body := []byte("{" +
		"\"object\":\"list\"," +
		"\"data\":[" +
		"{\"id\":\"claude-opus-4.8\",\"name\":\"Claude Opus 4.8\",\"model_picker_enabled\":true,\"capabilities\":{\"type\":\"chat\",\"family\":\"claude-opus-4.8\",\"supports\":{\"streaming\":true,\"tool_calls\":true}}}," +
		"{\"id\":\"gpt-5.5\",\"name\":\"GPT-5.5\",\"model_picker_enabled\":true,\"capabilities\":{\"type\":\"chat\",\"family\":\"gpt-5.5\",\"supports\":{\"streaming\":true,\"tool_calls\":true}}}," +
		"{\"id\":\"trajectory-compaction\",\"name\":\"Trajectory Compaction\",\"model_picker_enabled\":false,\"capabilities\":{\"type\":\"chat\"}}," +
		"{\"id\":\"text-embedding-3-small\",\"name\":\"Embedding V3 small\",\"model_picker_enabled\":false,\"capabilities\":{\"type\":\"embeddings\"}}" +
		"]" +
		"}")
	models, err := PickerChatModelsFromCatalog(body)
	if err != nil {
		t.Fatalf("parse catalog: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 picker chat models, got %#v", models)
	}
	if models[0].Alias != "copilot-claude-opus-4.8" || models[0].ModelID != "claude-opus-4.8" || models[0].DisplayName != "Claude Opus 4.8" {
		t.Fatalf("unexpected first model: %#v", models[0])
	}
	if models[1].Alias != "copilot-gpt-5.5" || models[1].ModelID != "gpt-5.5" {
		t.Fatalf("unexpected second model: %#v", models[1])
	}
}

func TestFindPickerChatModelByAlias(t *testing.T) {
	body := []byte("{\"data\":[{\"id\":\"claude-opus-4.8\",\"name\":\"Claude Opus 4.8\",\"model_picker_enabled\":true,\"capabilities\":{\"type\":\"chat\"}}]}")
	model, ok, err := FindPickerChatModelByAlias(body, "copilot-claude-opus-4.8")
	if err != nil {
		t.Fatalf("find model: %v", err)
	}
	if !ok || model.ModelID != "claude-opus-4.8" {
		t.Fatalf("expected dynamic model match, got ok=%v model=%#v", ok, model)
	}
	if _, ok, err := FindPickerChatModelByAlias(body, "copilot-missing"); err != nil || ok {
		t.Fatalf("expected missing model, got ok=%v err=%v", ok, err)
	}
}

func TestGovernedModelAliasSanitizesIDs(t *testing.T) {
	if got := GovernedModelAlias("Claude Opus/4.8 Preview"); got != "copilot-claude-opus-4.8-preview" {
		t.Fatalf("unexpected alias: %s", got)
	}
}
