package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"ai-agent-orch/internal/catalog"
)

func main() {
	root := flag.String("catalog-root", ".", "catalog root directory")
	flag.Parse()

	report, err := catalog.Validate(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "catalog validation failed: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
		os.Exit(1)
	}
}
