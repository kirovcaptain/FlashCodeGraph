package main

import (
	"fmt"
	"os"

	"github.com/liuymcn/flash-code-graph/internal/gateway/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
