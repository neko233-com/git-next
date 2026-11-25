package main

import (
	"fmt"
	"os"

	"github.com/neko233-com/git-next/internal/cli"
	"github.com/neko233-com/git-next/internal/gitx"
	"github.com/neko233-com/git-next/internal/i18n"
)

func fail(err error) {
	if code, ok := gitx.ExitCode(err); ok {
		os.Exit(code)
	}
	fmt.Fprintln(os.Stderr, "fatal:", err)
	os.Exit(1)
}

func main() {
	if err := i18n.Initialize(os.Args); err != nil {
		fail(err)
	}
	inv, err := cli.Parse(os.Args)
	if err != nil {
		fail(err)
	}
	for _, dir := range inv.Cwd {
		if err := os.Chdir(dir); err != nil {
			fail(err)
		}
	}
	if inv.Version {
		fmt.Printf("git-next version %s (experimental)\n", gitx.Version)
		return
	}
	if inv.Help {
		fmt.Print(cli.HelpText(inv.Command))
		return
	}
	if inv.Command == "" {
		fmt.Print(cli.HelpText(""))
		return
	}
	spec, ok := cli.Specs[inv.Command]
	if !ok {
		fail(fmt.Errorf("command %q has no flag specification", inv.Command))
	}
	for _, arg := range inv.Args {
		if arg == "--help" || arg == "-h" {
			fmt.Print(cli.HelpText(inv.Command))
			return
		}
	}
	parsed, err := cli.ParseCommand(spec, inv.Args)
	if err != nil {
		fail(err)
	}
	ctx, err := gitx.NewContext(inv)
	if err != nil {
		fail(err)
	}
	if err := ctx.Dispatch(parsed); err != nil {
		fail(err)
	}
}
