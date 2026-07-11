package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/hooklane"
)

// hookLifecycles maps the subcommand argument to its lifecycle.
var hookLifecycles = map[string]hooklane.Lifecycle{
	"prompt-submit": hooklane.PromptSubmit,
	"post-tool":     hooklane.PostTool,
	"stop":          hooklane.Stop,
}

// handleHook dispatches `ai-orch hook <lifecycle>`, reading the lifecycle event
// JSON from stdin and posting directly to the Governance Shell REST API.
func handleHook(ctx context.Context, cfg Config, args []string) {
	if code := runHook(ctx, cfg, args, os.Stdin, os.Stderr, hooklane.Workspace{Dir: "."}); code != 0 {
		os.Exit(code)
	}
}

// runHook is the testable core of the hook command. It returns the process
// exit code instead of calling os.Exit, so dispatch can be exercised without
// spawning a process: 0 success (including when the current event was
// queued to the local spool after a network/5xx failure -- see Run), 2 usage
// error, 1 runtime error (a 4xx rejection or an unrecoverable local error).
func runHook(ctx context.Context, cfg Config, args []string, stdin io.Reader, stderr io.Writer, ws hooklane.Workspace) int {
	if len(args) == 0 {
		hookUsage(stderr)
		return 2
	}
	lc, ok := hookLifecycles[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown hook lifecycle: %s\n", args[0])
		hookUsage(stderr)
		return 2
	}

	event, err := hooklane.ParseEvent(stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	client := hooklane.NewClient(cfg.GovernanceURL, cfg.Token)
	result, err := hooklane.Run(ctx, client, lc, event, ws)
	for _, note := range result.Notes {
		fmt.Fprintln(stderr, note)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if result.Spooled {
		fmt.Fprintln(stderr, "evidence queued for retry: the governance shell was unreachable or returned a server error")
	}
	return 0
}

func hookUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: ai-orch hook prompt-submit|post-tool|stop  (lifecycle event JSON on stdin)")
}
