// Package logx configures slog for the services and provides printf-style
// helpers so call sites stay compact while every line gets a level and
// timestamp, and can be emitted as JSON for log pipelines.
package logx

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Setup installs the default slog handler. Plain text is the default;
// AI_ORCH_LOG_FORMAT=json switches to one JSON object per line.
func Setup() {
	var handler slog.Handler
	if strings.EqualFold(os.Getenv("AI_ORCH_LOG_FORMAT"), "json") {
		handler = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		handler = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(handler))
}

// Infof logs a formatted message at info level.
func Infof(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}

// Warnf logs a formatted message at warn level.
func Warnf(format string, args ...any) {
	slog.Warn(fmt.Sprintf(format, args...))
}

// Errorf logs a formatted message at error level.
func Errorf(format string, args ...any) {
	slog.Error(fmt.Sprintf(format, args...))
}

// Fatalf logs a formatted message at error level and exits.
func Fatalf(format string, args ...any) {
	slog.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}

// Fatal logs the arguments at error level and exits.
func Fatal(args ...any) {
	slog.Error(fmt.Sprint(args...))
	os.Exit(1)
}
