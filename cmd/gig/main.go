// gig is a CLI tool for generating Gig dependency packages and inspecting
// interpreted programs.
//
// # Commands
//
//	gig init -package <name>    Create a new dependency package directory
//	gig gen <dir>               Generate registration code from <dir>/pkgs.go
//	gig dump <file|->           Print readable SSA
//
// # Workflow
//
//  1. Initialize a dependency package:
//
//     gig init -package mydep
//
//  2. Edit pkgs.go to add your imports:
//
//     package mydep
//
//     import (
//     _ "encoding/json"
//     _ "fmt"
//     _ "github.com/spf13/cast"
//     )
//
//  3. Generate registration code:
//
//     gig gen ./mydep
//
//  4. Import in your program:
//
//     import _ "your/module/mydep/packages"
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/t04dJ14n9/gig/cmd/gig/commands"
)

// command defines a CLI subcommand with its own flag set and execution logic.
type command struct {
	Name  string
	Usage string
	Run   func(fs *flag.FlagSet, args []string) error
}

func main() {
	cmds := []command{
		{Name: "init", Usage: "gig init -package <name>", Run: commands.RunInit},
		{Name: "gen", Usage: "gig gen <dir>", Run: commands.RunGen},
		{Name: "dump", Usage: "gig dump <file|-> | --raw <source>", Run: commands.RunDump},
	}

	flag.Usage = printUsage(cmds)

	if len(os.Args) < 2 {
		flag.Usage()
		os.Exit(1)
	}

	for _, cmd := range cmds {
		if cmd.Name == os.Args[1] {
			fs := flag.NewFlagSet(cmd.Name, flag.ExitOnError)
			if err := cmd.Run(fs, os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
	flag.Usage()
	os.Exit(1)
}

func printUsage(cmds []command) func() {
	return func() {
		fmt.Fprintln(os.Stderr, "gig - generate Gig dependency packages and inspect interpreted programs")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  gig <command> [arguments]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		for _, cmd := range cmds {
			fmt.Fprintf(os.Stderr, "  %s\n", cmd.Usage)
		}
		fmt.Fprintf(os.Stderr, "\nWorkflow:\n")
		fmt.Fprintf(os.Stderr, "  1. gig init -package mydep         # Creates mydep/pkgs.go\n")
		fmt.Fprintf(os.Stderr, "  2. Edit mydep/pkgs.go              # Add third-party libraries\n")
		fmt.Fprintf(os.Stderr, "  3. gig gen ./mydep                 # Generate registration code\n")
		fmt.Fprintf(os.Stderr, "  4. import _ \"myapp/mydep/packages\"      # Use in your program\n")
		fmt.Fprintf(os.Stderr, "\nDebugging:\n")
		fmt.Fprintf(os.Stderr, "  gig dump program.go                # Print SSA\n")
		fmt.Fprintf(os.Stderr, "  gig dump --raw 'package main...'   # Dump inline source\n")
	}
}
