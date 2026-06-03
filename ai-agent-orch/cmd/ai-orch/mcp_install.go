package main

import (
	"flag"
	"fmt"
	"os"

	"ai-agent-orch/internal/skillsfactory"
)

func handleMCPInstall(cfg Config, args []string) {
	fs := flag.NewFlagSet("mcp install", flag.ExitOnError)
	clientFlag := fs.String("client", "", "Client to install for (vscode, cline, claude-code, codex)")
	force := fs.Bool("force", false, "Overwrite existing client configuration files")
	_ = fs.Parse(args)

	if *clientFlag == "" {
		fmt.Fprintln(os.Stderr, "usage: ai-orch mcp install --client <vscode|cline|claude-code|codex> [--force]")
		os.Exit(1)
	}

	client, err := skillsfactory.ParseClientType(*clientFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid client: %v\n", err)
		os.Exit(1)
	}

	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get working directory: %v\n", err)
		os.Exit(1)
	}

	result, err := skillsfactory.InstallWithOptions(client, dir, cfg.GovernanceURL, skillsfactory.InstallOptions{Force: *force})
	if err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Files written:")
	for _, f := range result.FilesWritten {
		fmt.Printf("  %s\n", f)
	}
	fmt.Println()
	fmt.Println(result.Instructions)
}

func handleMCPDoctor(cfg Config, args []string) {
	fs := flag.NewFlagSet("mcp doctor", flag.ExitOnError)
	_ = fs.Parse(args)

	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get working directory: %v\n", err)
		os.Exit(1)
	}

	issues := skillsfactory.Doctor(dir, cfg.GovernanceURL)
	fmt.Println("MCP Doctor — Client Config Check")
	fmt.Println()
	for _, issue := range issues {
		fmt.Printf("- %s\n", issue)
	}
}
