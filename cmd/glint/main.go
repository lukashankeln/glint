package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/pprof"
	"time"

	"github.com/lukashankeln/glint/internal/cli"
)

func main() {
	perf := os.Getenv("GLINT_PERF") != ""
	start := time.Now()

	if p := os.Getenv("GLINT_CPUPROFILE"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			panic(err)
		}
		pprof.StartCPUProfile(f)
	}

	code := run()

	pprof.StopCPUProfile()

	if p := os.Getenv("GLINT_MEMPROFILE"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			panic(err)
		}
		pprof.WriteHeapProfile(f)
		f.Close()
	}

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
