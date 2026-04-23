package main

import (
	"fmt"
	"os"

	"github.com/kirovcaptain/FlashCodeGraph/internal/gateway/cli"
	"github.com/kirovcaptain/FlashCodeGraph/skills"
)

func main() {
	cli.SkillsFS = skills.FS()

	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
