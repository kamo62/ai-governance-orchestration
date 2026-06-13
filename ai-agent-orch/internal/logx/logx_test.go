package logx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestSetupSelectsHandlerFormat(t *testing.T) {
	original := slog.Default()
	defer slog.SetDefault(original)

	t.Setenv("AI_ORCH_LOG_FORMAT", "json")
	Setup()
	if _, ok := slog.Default().Handler().(*slog.JSONHandler); !ok {
		t.Fatalf("handler = %T, want *slog.JSONHandler", slog.Default().Handler())
	}

	t.Setenv("AI_ORCH_LOG_FORMAT", "")
	Setup()
	if _, ok := slog.Default().Handler().(*slog.TextHandler); !ok {
		t.Fatalf("handler = %T, want *slog.TextHandler", slog.Default().Handler())
	}
}

func TestFormattedHelpersLogAtExpectedLevel(t *testing.T) {
	original := slog.Default()
	defer slog.SetDefault(original)

	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	Infof("hello %s", "info")
	Warnf("hello %s", "warn")
	Errorf("hello %s", "error")

	want := []struct {
		level string
		msg   string
	}{
		{"INFO", "hello info"},
		{"WARN", "hello warn"},
		{"ERROR", "hello error"},
	}
	dec := json.NewDecoder(&buf)
	for _, w := range want {
		var line struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		if err := dec.Decode(&line); err != nil {
			t.Fatalf("decode log line for %s: %v", w.level, err)
		}
		if line.Level != w.level || line.Msg != w.msg {
			t.Fatalf("line = %+v, want level=%s msg=%q", line, w.level, w.msg)
		}
	}
}
