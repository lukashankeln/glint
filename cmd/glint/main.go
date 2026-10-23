package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/lukashankeln/glint/internal/cli"
)

func main() {
	perf := os.Getenv("GLINT_PERF") != ""
	start := time.Now()

	code := run()

	if perf {
		printPerfStats(time.Since(start))
	}
	os.Exit(code)
}

func run() int {
	if err := cli.Execute(); err != nil {
		if _, ok := errors.AsType[*cli.ViolationsFoundError](err); ok {
			return cli.ExitViolations
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return cli.ExitError
	}
	return cli.ExitOK
}
