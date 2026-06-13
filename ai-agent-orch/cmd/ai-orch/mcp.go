package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/appversion"
	"github.com/kamo62/ai-governance-orchestration/ai-agent-orch/internal/mcpgateway"
)

func handleMCPStart(ctx context.Context, cfg Config, args []string) {
	fs := flag.NewFlagSet("mcp start", flag.ExitOnError)
	transport := fs.String("transport", "http", "Transport type: http or stdio")
	host := fs.String("host", "127.0.0.1", "HTTP bind host (for http transport)")
	port := fs.String("port", "18081", "HTTP port (for http transport)")
	_ = fs.Parse(args)

	mcp := mcpgateway.NewServer("ai-orch-mcp", appversion.Version)
	gatewayCfg := &mcpgateway.GatewayConfig{
		GovernanceURL:      cfg.GovernanceURL,
		DevToken:           cfg.Token,
		TrustedClientToken: cfg.TrustedClientToken,
	}
	mcpgateway.RegisterPhase1GTools(mcp, gatewayCfg)
	mcpgateway.RegisterControlPlaneTools(mcp, gatewayCfg)
	mcpgateway.RegisterPhase1JTools(mcp, gatewayCfg)

	switch *transport {
	case "http":
		httpServer := mcpgateway.NewHTTPServer(mcp, cfg.Token)
		mux := http.NewServeMux()
		httpServer.RegisterRoutes(mux)
		addr := net.JoinHostPort(*host, *port)
		server := &http.Server{Addr: addr, Handler: mux}

		go func() {
			fmt.Printf("MCP gateway listening on http://%s\n", addr)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "server error: %v\n", err)
				os.Exit(1)
			}
		}()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nShutting down MCP gateway...")
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "shutdown error: %v\n", err)
			os.Exit(1)
		}

	case "stdio":
		stdio := mcpgateway.NewStdioTransport(mcp)
		if err := stdio.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "stdio transport error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown transport: %s (use http or stdio)\n", *transport)
		os.Exit(1)
	}
}
