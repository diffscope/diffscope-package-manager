package main

import (
	"fmt"
	"os"

	"diffscope-package-manager/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
