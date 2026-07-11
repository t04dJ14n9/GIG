// Package commands implements the CLI subcommands for the gig tool.
package commands

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/t04dJ14n9/gig"
	_ "github.com/t04dJ14n9/gig/stdlib/packages"
)

// RunDump implements the "gig dump" subcommand.
func RunDump(fs *flag.FlagSet, args []string) error {
	allowPanic := fs.Bool("allow-panic", false, "allow panic/recover/defer compilation while dumping")
	rawSource := fs.String("raw", "", "raw source code to dump instead of reading <file|->")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: gig dump [flags] <file|->\n")
		fmt.Fprintf(os.Stderr, "       gig dump --raw <source>\n\n")
		fmt.Fprintf(os.Stderr, "Compiles Gig source and prints readable SSA.\n")
		fmt.Fprintf(os.Stderr, "Use '-' to read source from stdin.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *rawSource != "" {
		if fs.NArg() != 0 {
			return fmt.Errorf("--raw cannot be used with a source file argument")
		}
		return dumpSource(*rawSource, *allowPanic)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("source file argument required")
	}

	source, err := readDumpSource(fs.Arg(0))
	if err != nil {
		return err
	}
	return dumpSource(string(source), *allowPanic)
}

func dumpSource(source string, allowPanic bool) error {
	var opts []gig.BuildOption
	if allowPanic {
		opts = append(opts, gig.WithAllowPanic())
	}
	dump, err := gig.DebugDump(source, opts...)
	if err != nil {
		return err
	}
	fmt.Print(dump)
	return nil
}

func readDumpSource(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	// #nosec G304 -- The CLI explicitly reads the source path selected by the user.
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return source, nil
}
