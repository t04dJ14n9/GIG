// Package commands implements the CLI subcommands for the gig tool.
package commands

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"

	"github.com/t04dJ14n9/gig/cmd/gig/gentool"
)

// RunGen implements the "gig gen" subcommand.
func RunGen(fs *flag.FlagSet, args []string) error {
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: gig gen <dir>\n\n")
		fmt.Fprintf(os.Stderr, "Generates dependency registration code from <dir>/pkgs.go.\n")
		fmt.Fprintf(os.Stderr, "The generated files will be placed in <dir>/packages/.\n\n")
		fmt.Fprintf(os.Stderr, "After generation, import the package in your program:\n")
		fmt.Fprintf(os.Stderr, "  import _ \"<package-name>/packages\"\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("directory argument required")
	}

	pkgDir := fs.Arg(0)
	pkgsPath := filepath.Join(pkgDir, "pkgs.go")

	if _, err := os.Stat(pkgsPath); os.IsNotExist(err) {
		return fmt.Errorf("%s not found; run 'gig init -package %s' first", pkgsPath, filepath.Base(pkgDir))
	}

	importPaths, pkgName, err := parsePkgsGo(pkgsPath)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", pkgsPath, err)
	}

	if len(importPaths) == 0 {
		return fmt.Errorf("no imports found in %s", pkgsPath)
	}

	fmt.Printf("Generating dependency package %q:\n", pkgName)
	fmt.Printf("  source: %s\n", pkgsPath)
	fmt.Printf("  output: %s/packages/\n", pkgDir)
	fmt.Printf("  packages: %d\n\n", len(importPaths))

	packagesDir := filepath.Join(pkgDir, "packages")
	// #nosec G301 -- Generated Go package directories must be readable by build users.
	if err := os.MkdirAll(packagesDir, 0o755); err != nil {
		return fmt.Errorf("creating packages directory: %w", err)
	}

	const generatedPkgName = "packages"
	var generatedCount int
	for _, path := range importPaths {
		fmt.Printf("importing %s\n", path)
		if err := gentool.PackageImport(path, packagesDir, generatedPkgName); err != nil {
			fmt.Printf("Error importing %s: %s\n", path, err)
			continue
		}
		generatedCount++
	}

	fmt.Printf("\nGenerated %d packages\n", generatedCount)
	fmt.Printf("\nDone! Add this to your program:\n")
	fmt.Printf("  import _ %q\n", pkgName+"/packages")
	return nil
}

// parsePkgsGo reads a pkgs.go file and extracts the package name and import paths.
func parsePkgsGo(path string) ([]string, string, error) {
	imports, err := gentool.ParsePkgsFile(path)
	if err != nil {
		return nil, "", err
	}

	// Use go/parser to extract the package name reliably
	file, err := parser.ParseFile(
		token.NewFileSet(),
		path,
		nil,
		parser.PackageClauseOnly|parser.SkipObjectResolution,
	)
	if err != nil {
		return nil, "", fmt.Errorf("parsing package clause: %w", err)
	}

	pkgName := file.Name.Name
	if pkgName == "" {
		return nil, "", fmt.Errorf("could not find package declaration")
	}

	return imports, pkgName, nil
}
