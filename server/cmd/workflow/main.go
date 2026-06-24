package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ming-agents/server/workflow"
)

func main() {
	input := flag.String("input", "", "User input or path to file with input")
	flag.Parse()

	ctx := context.Background()
	repoRoot := os.Getenv("PWD")
	if *input != "" {
		if data, err := os.ReadFile(*input); err == nil {
			*input = string(data)
		}
	} else if flag.NArg() > 0 {
		*input = flag.Arg(0)
	}

	runID, err := workflow.Run(ctx, repoRoot, *input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Run failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("run_id: %s\n", runID)
	fmt.Println("Outputs: docs/requirements-clarity.md, docs/planning.md, docs/output.md")
}
