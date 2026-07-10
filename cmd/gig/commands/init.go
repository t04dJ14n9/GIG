// Package commands implements the CLI subcommands for the gig tool.
package commands

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"text/template"
)

//go:embed pkgs_template.go.tmpl
var pkgsTemplate string

// RunInit implements the "gig init" subcommand.
// It creates a new dependency package directory with a pkgs.go template.
func RunInit(fs *flag.FlagSet, args []string) error {
	var packageName string
	fs.StringVar(&packageName, "package", "", "Package name for the dependency (required)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: gig init -package <name>\n\n")
		fmt.Fprintf(os.Stderr, "Creates a directory <name>/ with pkgs.go containing stdlib imports.\n")
		fmt.Fprintf(os.Stderr, "Edit the file to add third-party libraries.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if packageName == "" {
		return fmt.Errorf("-package is required")
	}

	if !isValidPackageName(packageName) {
		return fmt.Errorf("%q is not a valid Go package name", packageName)
	}

	// #nosec G301 -- Generated Go package directories must be readable by build users.
	if err := os.MkdirAll(packageName, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	pkgsGo, err := generatePkgsGo(packageName)
	if err != nil {
		return fmt.Errorf("generating pkgs.go: %w", err)
	}

	pkgsPath := filepath.Join(packageName, "pkgs.go")
	// #nosec G306 -- Generated Go source uses standard permissions and respects umask.
	if err := os.WriteFile(pkgsPath, pkgsGo, 0o666); err != nil {
		return fmt.Errorf("writing pkgs.go: %w", err)
	}

	fmt.Printf("Created %s/\n", packageName)
	fmt.Printf("  %s\n", pkgsPath)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Edit %s to add third-party libraries\n", pkgsPath)
	fmt.Printf("  2. Run: gig gen ./%s\n", packageName)
	fmt.Printf("  3. Import in your program: import _ %q\n", packageName)
	return nil
}

// generatePkgsGo renders the pkgs.go template with the given package name.
func generatePkgsGo(pkgName string) ([]byte, error) {
	tmpl, err := template.New("pkgs").Parse(pkgsTemplate)
	if err != nil {
		return nil, fmt.Errorf("parsing template: %w", err)
	}

	data := struct{ PackageName string }{PackageName: pkgName}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return buf.Bytes(), nil //nolint:nilerr // Intentional fallback to unformatted code
	}
	return formatted, nil
}

// isValidPackageName checks if name is a valid Go package name.
func isValidPackageName(name string) bool {
	return token.IsIdentifier(name) && !token.Lookup(name).IsKeyword()
}
