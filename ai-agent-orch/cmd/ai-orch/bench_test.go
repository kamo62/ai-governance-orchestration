package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBenchmarkRunCallsAllEnabledModelsAndStoresEvidence(t *testing.T) {
	var modelCalls []string
	var evidenceCalls int

	modelGateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"coding-fast"},{"id":"copilot-gpt-5-mini"}]}`)
		case "/v1/chat/completions":
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			modelCalls = append(modelCalls, body.Model)
			w.Header().Set("X-AI-Orch-Session-ID", "sess_"+body.Model)
			fmt.Fprintf(w, `{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
		default:
			t.Fatalf("unexpected model gateway path %s", r.URL.Path)
		}
	}))
	defer modelGateway.Close()

	governance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/evidence" || r.Method != http.MethodPost {
			t.Fatalf("unexpected governance request %s %s", r.Method, r.URL.Path)
		}
		evidenceCalls++
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"evidence_1"}`)
	}))
	defer governance.Close()

	result, err := runBenchmark(t.Context(), Config{
		GovernanceURL:   governance.URL,
		ModelGatewayURL: modelGateway.URL,
		Token:           "dev-token",
		RuntimeToken:    "runtime-token",
	}, BenchmarkOptions{Workflow: "smoke", Models: "all-enabled"})
	if err != nil {
		t.Fatalf("run benchmark: %v", err)
	}
	if len(result.Results) != 2 || len(modelCalls) != 2 {
		t.Fatalf("expected two benchmark model calls, result=%#v calls=%#v", result, modelCalls)
	}
	if evidenceCalls != 2 {
		t.Fatalf("expected evidence per model result, got %d", evidenceCalls)
	}
	if result.Results[0].CostPerSuccessUSD != 0 {
		t.Fatalf("offline smoke has no cost and should report zero cost-per-success, got %#v", result.Results[0])
	}
}
