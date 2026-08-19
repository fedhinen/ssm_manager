package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/victorruiz/ssm-manager/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().ExecuteContext(context.Background()); err != nil {
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(1)
	}
}
