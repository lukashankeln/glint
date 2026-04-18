package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/lukashankeln/glint/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		var ve *cli.ViolationsFoundError
		if errors.As(err, &ve) {
			os.Exit(cli.ExitViolations)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(cli.ExitError)
	}
}
