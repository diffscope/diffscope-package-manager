package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/diffscope/diffscope-package-manager/internal/cli"
	"github.com/diffscope/diffscope-package-manager/internal/cli/output"
)

func main() {
	if err := cli.Execute(); err != nil {
		var reported *output.ReportedError
		if !errors.As(err, &reported) {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(1)
	}
}
