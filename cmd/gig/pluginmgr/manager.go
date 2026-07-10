// Package pluginmgr provides hot-loading of external Go packages via the
// Go plugin system. This allows users to import third-party libraries
// directly in the REPL without manual code generation.
//
// # Platform Support
//
// Plugin loading is only supported on Linux and macOS.
// Windows users will receive an error message suggesting alternatives.
//
// # Architecture
//
// The plugin system works by:
//  1. Detecting new import statements in REPL
//  2. Downloading packages with `go get`
//  3. Generating wrapper code (similar to gentool)
//  4. Compiling as a `.so` shared library
//  5. Loading with `plugin.Open()`
//  6. Calling the registration function to register with gig
//
// # Cache Directory
//
// Plugins are cached in ~/.gig/plugins/ with the following structure:
//
//	~/.gig/plugins/
//	├── github.com/
//	│   └── spf13/
//	│       └── cast/
//	│           ├── cast.so      # Compiled plugin
//	│           └── wrapper.go   # Generated wrapper code
//	└── go.mod                   # Plugin module file
package pluginmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/t04dJ14n9/gig"
)

// Plugin errors
var (
	ErrPluginNotSupported = errors.New("plugin loading requires Linux or macOS; Windows is not supported")
	ErrPackageNotFound    = errors.New("package not found; run 'go get <package>' first")
	ErrPluginBuildFailed  = errors.New("failed to build plugin")
)

const defaultPluginCommandTimeout = 5 * time.Minute

// Manager manages hot-loading of external packages.
type Manager struct {
	mu             sync.RWMutex
	pluginDir      string                      // ~/.gig/plugins
	loaded         map[string]bool             // Set of loaded package paths
	registry       map[string]pluginMetadata   // Package path -> metadata
	symbols        map[string]*ExportedSymbols // Package path -> cached symbols
	commandTimeout time.Duration
}

// pluginMetadata stores information about a loaded plugin.
type pluginMetadata struct {
	Path      string `json:"path"`       // Package import path
	SOPath    string `json:"so_path"`    // Path to .so file
	BuildTime string `json:"build_time"` // When it was built
}

// NewManager creates a new plugin manager.
func NewManager() *Manager {
	pluginDir := getPluginDir()
	pm := &Manager{
		pluginDir:      pluginDir,
		loaded:         make(map[string]bool),
		registry:       make(map[string]pluginMetadata),
		symbols:        make(map[string]*ExportedSymbols),
		commandTimeout: defaultPluginCommandTimeout,
	}
	pm.loadRegistry()
	return pm
}

// getPluginDir returns the plugin cache directory.
func getPluginDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = os.TempDir()
	}
	return filepath.Join(homeDir, ".gig", "plugins")
}

// loadRegistry loads the plugin registry from disk.
func (pm *Manager) loadRegistry() {
	registryPath := filepath.Join(pm.pluginDir, "plugin_registry.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return
	}
	//nolint:errchkjson // Registry file is internal, ignore unmarshal errors
	_ = json.Unmarshal(data, &pm.registry)
}

// saveRegistry saves the plugin registry to disk.
func (pm *Manager) saveRegistry() error {
	if err := os.MkdirAll(pm.pluginDir, 0o755); err != nil {
		return err
	}
	registryPath := filepath.Join(pm.pluginDir, "plugin_registry.json")
	data := mustMarshalJSON(pm.registry)
	return os.WriteFile(registryPath, data, 0o644)
}

// mustMarshalJSON marshals to JSON, panicking on error (should never happen for registry).
func mustMarshalJSON(v any) []byte {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return data
}

// LoadPackage attempts to load an external package.
// It checks if the package is already registered, then attempts to load
// it via the plugin system if supported by the platform.
func (pm *Manager) LoadPackage(pkgPath string) error {
	timeout := pm.commandTimeout
	if timeout <= 0 {
		timeout = defaultPluginCommandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return pm.LoadPackageContext(ctx, pkgPath)
}

// LoadPackageContext loads an external package while bounding package
// discovery, dependency download, and plugin build commands with ctx.
func (pm *Manager) LoadPackageContext(ctx context.Context, pkgPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Check if already registered in gig
	if gig.GetPackageByPath(pkgPath) != nil {
		return nil
	}

	// Check platform support
	if runtime.GOOS == "windows" {
		return ErrPluginNotSupported
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if already loaded
	if pm.loaded[pkgPath] {
		return nil
	}

	// Check if we have a cached plugin
	if meta, ok := pm.registry[pkgPath]; ok {
		if _, err := os.Stat(meta.SOPath); err == nil {
			// Plugin exists, try to load it
			if err := pm.loadPlugin(meta.SOPath, pkgPath); err == nil {
				pm.loaded[pkgPath] = true
				return nil
			}
			// If loading failed, rebuild
		}
	}

	// Build and load the plugin
	return pm.buildAndLoad(ctx, pkgPath)
}

// buildAndLoad downloads, builds, and loads a package as a plugin.
func (pm *Manager) buildAndLoad(ctx context.Context, pkgPath string) error {
	// Step 1: Ensure package is downloaded
	if err := pm.downloadPackage(ctx, pkgPath); err != nil {
		return err
	}

	// Step 2: Generate wrapper code
	wrapperPath, err := pm.generateWrapper(ctx, pkgPath)
	if err != nil {
		return fmt.Errorf("generate wrapper: %w", err)
	}

	// Step 3: Build plugin
	soPath, err := pm.buildPlugin(ctx, pkgPath, wrapperPath)
	if err != nil {
		return fmt.Errorf("build plugin: %w", err)
	}

	// Step 4: Load plugin
	if err := pm.loadPlugin(soPath, pkgPath); err != nil {
		return fmt.Errorf("load plugin: %w", err)
	}

	// Step 5: Update registry
	pm.loaded[pkgPath] = true
	pm.registry[pkgPath] = pluginMetadata{
		Path:      pkgPath,
		SOPath:    soPath,
		BuildTime: fmt.Sprintf("%d", getCurrentTime()),
	}
	_ = pm.saveRegistry() // Best effort, ignore error

	return nil
}

// downloadPackage ensures the package is available locally.
func (pm *Manager) downloadPackage(ctx context.Context, pkgPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Try to get package info first
	cmd := exec.CommandContext(ctx, "go", "list", pkgPath)
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Package not found, try to download
		cmd = exec.CommandContext(ctx, "go", "get", pkgPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("%w: %s", ErrPackageNotFound, pkgPath)
		}
	}
	return nil
}

// generateWrapper generates plugin wrapper code for a package.
func (pm *Manager) generateWrapper(ctx context.Context, pkgPath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Create package directory
	pkgDir := filepath.Join(pm.pluginDir, strings.ReplaceAll(pkgPath, "/", string(filepath.Separator)))
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return "", err
	}

	// Generate wrapper code using simplified approach
	// We generate code that imports the package and registers exported symbols
	code, err := pm.generatePluginCode(ctx, pkgPath)
	if err != nil {
		return "", err
	}

	wrapperPath := filepath.Join(pkgDir, "wrapper.go")
	if err := os.WriteFile(wrapperPath, code, 0o644); err != nil {
		return "", err
	}

	return wrapperPath, nil
}

// generatePluginCode generates the Go source code for a plugin.
// It uses go/types to discover exported symbols and generates registration code.
func (pm *Manager) generatePluginCode(ctx context.Context, pkgPath string) ([]byte, error) {
	pkgAlias := sanitizePkgNameForImport(pkgPath)
	pkgBaseName := filepath.Base(pkgPath)

	// Get exported symbols from the package
	symbols, err := pm.getExportedSymbols(ctx, pkgPath)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// If we can't get symbols, generate minimal code
		return pm.generateReflectPluginCode(ctx, pkgPath, pkgAlias, pkgBaseName)
	}

	// If no symbols found, use fallback
	if len(symbols.Funcs) == 0 && len(symbols.Types) == 0 && len(symbols.Consts) == 0 && len(symbols.Vars) == 0 {
		return pm.generateReflectPluginCode(ctx, pkgPath, pkgAlias, pkgBaseName)
	}

	// Cache the symbols for tab completion
	// NOTE: caller (LoadPackage→buildAndLoad) already holds pm.mu,
	// so we must NOT lock here — sync.Mutex is not reentrant.
	pm.symbols[pkgPath] = symbols

	var sb strings.Builder
	sb.WriteString("// Code generated by gig plugin manager. DO NOT EDIT.\n")
	sb.WriteString("package main\n\n")

	// Only import reflect if we have types to register
	needReflect := len(symbols.Types) > 0

	sb.WriteString("import (\n")
	_, _ = fmt.Fprintf(&sb, "\t%s %q\n", pkgAlias, pkgPath)
	if needReflect {
		sb.WriteString("\t\"reflect\"\n")
	}
	sb.WriteString("\n")
	sb.WriteString("\t\"github.com/t04dJ14n9/gig/importer\"\n")
	sb.WriteString(")\n\n")

	// Generate Register function
	sb.WriteString("// Register is called by the plugin host to register this package.\n")
	sb.WriteString("func Register() {\n")
	_, _ = fmt.Fprintf(&sb, "\tpkg := importer.RegisterPackage(%q, %q)\n\n", pkgPath, pkgBaseName)

	// Register functions
	if len(symbols.Funcs) > 0 {
		sb.WriteString("\t// Functions\n")
		for _, fn := range symbols.Funcs {
			_, _ = fmt.Fprintf(&sb, "\tpkg.AddFunction(%q, %s.%s, \"\", nil)\n", fn, pkgAlias, fn)
		}
		sb.WriteString("\n")
	}

	// Register constants
	if len(symbols.Consts) > 0 {
		sb.WriteString("\t// Constants\n")
		for _, c := range symbols.Consts {
			_, _ = fmt.Fprintf(&sb, "\tpkg.AddConstant(%q, %s.%s, \"\")\n", c, pkgAlias, c)
		}
		sb.WriteString("\n")
	}

	// Register variables
	if len(symbols.Vars) > 0 {
		sb.WriteString("\t// Variables\n")
		for _, v := range symbols.Vars {
			_, _ = fmt.Fprintf(&sb, "\tpkg.AddVariable(%q, &%s.%s, \"\")\n", v, pkgAlias, v)
		}
		sb.WriteString("\n")
	}

	// Register types
	if len(symbols.Types) > 0 {
		sb.WriteString("\t// Types\n")
		for _, t := range symbols.Types {
			_, _ = fmt.Fprintf(&sb, "\tpkg.AddType(%q, reflect.TypeOf((*%s.%s)(nil)).Elem(), \"\")\n", t, pkgAlias, t)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\t_ = pkg // Suppress unused variable warning\n")
	sb.WriteString("}\n\n")

	// Generate main function for plugin compatibility
	sb.WriteString("func main() {}\n")

	code, err := format.Source([]byte(sb.String()))
	if err != nil {
		// Return unformatted code instead of error - it's still valid Go
		return []byte(sb.String()), nil //nolint:nilerr // Intentional fallback to unformatted code
	}
	return code, nil
}

// ExportedSymbols holds discovered exported symbols from a package.
type ExportedSymbols struct {
	Funcs  []string
	Consts []string
	Vars   []string
	Types  []string
}

// getExportedSymbols uses go doc to discover exported symbols.
func (pm *Manager) getExportedSymbols(ctx context.Context, pkgPath string) (*ExportedSymbols, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Use go doc to get package documentation which lists exports
	cmd := exec.CommandContext(ctx, "go", "doc", "-short", pkgPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("go doc failed: %w", err)
	}

	symbols := &ExportedSymbols{}
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Parse function signatures: func Name(...) or func Name[T ...](...)
		if strings.HasPrefix(line, "func ") {
			rest := strings.TrimPrefix(line, "func ")
			// Check if this is a generic function (has [ before first ()
			hasTypeParams := false
			for _, c := range rest {
				if c == '[' {
					hasTypeParams = true
					break
				}
				if c == '(' {
					break
				}
			}

			// Skip generic functions - they can't be passed as function values
			if hasTypeParams {
				continue
			}

			// Find the function name - it ends at (
			var name string
			for i, c := range rest {
				if c == '(' {
					name = rest[:i]
					break
				}
			}
			name = strings.TrimSpace(name)
			if isExported(name) {
				symbols.Funcs = append(symbols.Funcs, name)
			}
		}

		// Parse type declarations: type Name ...
		if strings.HasPrefix(line, "type ") {
			rest := strings.TrimPrefix(line, "type ")
			// Find the type name - it ends at space, [, or {
			var name string
			for i, c := range rest {
				if c == ' ' || c == '[' || c == '{' {
					name = rest[:i]
					break
				}
			}
			if name == "" {
				name = strings.Fields(rest)[0]
			}
			name = strings.TrimSpace(name)

			// Skip type constraints (interfaces with type constraints)
			// These appear as "type Name interface { ... }" with type constraints
			if strings.Contains(rest, "interface{") || strings.Contains(rest, "interface {") {
				// Skip type constraint interfaces
				continue
			}

			if isExported(name) {
				symbols.Types = append(symbols.Types, name)
			}
		}

		// Parse constants: const Name = ... or const ( block )
		if strings.HasPrefix(line, "const ") {
			rest := strings.TrimPrefix(line, "const ")
			name := strings.Fields(rest)[0]
			if isExported(name) {
				symbols.Consts = append(symbols.Consts, name)
			}
		}

		// Parse variables: var Name = ... or var ( block )
		if strings.HasPrefix(line, "var ") {
			rest := strings.TrimPrefix(line, "var ")
			name := strings.Fields(rest)[0]
			if isExported(name) {
				symbols.Vars = append(symbols.Vars, name)
			}
		}
	}

	// If no symbols found, return error to fall back to reflect approach
	if len(symbols.Funcs) == 0 && len(symbols.Types) == 0 && len(symbols.Consts) == 0 && len(symbols.Vars) == 0 {
		return nil, fmt.Errorf("no symbols discovered")
	}

	return symbols, nil
}

// generateReflectPluginCode generates minimal code that just registers the package.
// This is a fallback when symbol discovery fails.
func (pm *Manager) generateReflectPluginCode(
	ctx context.Context,
	pkgPath, pkgAlias, pkgBaseName string,
) ([]byte, error) {
	// Try to find at least one exported symbol for the blank import reference
	symbolToUse := ""
	if symbols, err := pm.getExportedSymbols(ctx, pkgPath); err == nil {
		switch {
		case len(symbols.Funcs) > 0:
			symbolToUse = symbols.Funcs[0]
		case len(symbols.Types) > 0:
			symbolToUse = symbols.Types[0]
		case len(symbols.Consts) > 0:
			symbolToUse = symbols.Consts[0]
		case len(symbols.Vars) > 0:
			symbolToUse = symbols.Vars[0]
		}
	} else if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}

	var sb strings.Builder
	sb.WriteString("// Code generated by gig plugin manager. DO NOT EDIT.\n")
	sb.WriteString("package main\n\n")

	sb.WriteString("import (\n")
	_, _ = fmt.Fprintf(&sb, "\t%s %q\n", pkgAlias, pkgPath)
	sb.WriteString("\n")
	sb.WriteString("\t\"github.com/t04dJ14n9/gig/importer\"\n")
	sb.WriteString(")\n\n")

	sb.WriteString("// Register is called by the plugin host to register this package.\n")
	sb.WriteString("func Register() {\n")
	_, _ = fmt.Fprintf(&sb, "\t_ = importer.RegisterPackage(%q, %q)\n", pkgPath, pkgBaseName)
	sb.WriteString("}\n\n")

	// Use a blank import reference if we found a symbol
	if symbolToUse != "" {
		sb.WriteString("// Reference an exported symbol to suppress unused import error\n")
		_, _ = fmt.Fprintf(&sb, "var _ = %s.%s\n", pkgAlias, symbolToUse)
	} else {
		// No symbols found - this shouldn't happen for valid packages
		// Generate a runtime error instead
		sb.WriteString("// No exported symbols found - this plugin may not work correctly\n")
		_, _ = fmt.Fprintf(&sb, "var _ = %q\n", pkgPath)
	}

	sb.WriteString("\nfunc main() {}\n")

	code, err := format.Source([]byte(sb.String()))
	if err != nil {
		// Return unformatted code instead of error - it's still valid Go
		return []byte(sb.String()), nil //nolint:nilerr // Intentional fallback to unformatted code
	}
	return code, nil
}

// isExported checks if a name is exported (starts with uppercase).
func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

// sanitizePkgNameForImport creates a valid Go identifier for package import alias.
func sanitizePkgNameForImport(path string) string {
	// Replace special characters to create a valid identifier
	result := strings.NewReplacer(
		"/", "_",
		"-", "_",
		".", "_",
	).Replace(path)
	// Ensure it starts with a letter
	if len(result) > 0 && (result[0] < 'a' || result[0] > 'z') && (result[0] < 'A' || result[0] > 'Z') {
		result = "pkg_" + result
	}
	return result
}

// buildPlugin compiles the wrapper as a .so shared library.
func (pm *Manager) buildPlugin(ctx context.Context, pkgPath, wrapperPath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	pkgDir := filepath.Dir(wrapperPath)
	soPath := filepath.Join(pkgDir, filepath.Base(pkgPath)+".so")

	// Create go.mod in plugin directory if it doesn't exist
	goModPath := filepath.Join(pkgDir, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, "go", "mod", "init", "gig-plugin-"+filepath.Base(pkgPath))
		cmd.Dir = pkgDir
		if err := cmd.Run(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", ctxErr
			}
			return "", fmt.Errorf("init go.mod: %w", err)
		}

		// Add replace directive to use local gig source
		// This is crucial for plugin compatibility
		gigRoot, err := findGigRoot(ctx)
		if err != nil {
			return "", err
		}
		if gigRoot != "" {
			replaceCmd := exec.CommandContext(ctx, "go", "mod", "edit", "-replace", "github.com/t04dJ14n9/gig="+gigRoot)
			replaceCmd.Dir = pkgDir
			if err := replaceCmd.Run(); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return "", ctxErr
				}
				return "", fmt.Errorf("add replace directive: %w", err)
			}
		}
	}

	// Download dependencies
	cmd := exec.CommandContext(ctx, "go", "mod", "tidy")
	cmd.Dir = pkgDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("go mod tidy: %w", err)
	}

	// Build plugin
	cmd = exec.CommandContext(ctx, "go", "build", "-buildmode=plugin", "-o", soPath, wrapperPath)
	cmd.Dir = pkgDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("%w: %w", ErrPluginBuildFailed, err)
	}

	return soPath, nil
}

// findGigRoot finds the root directory of the gig module.
// This is needed to add a replace directive in plugin go.mod files.
func findGigRoot(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Try to find gig root from current directory
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Dir}}", "github.com/t04dJ14n9/gig")
	output, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", nil
	}
	return strings.TrimSpace(string(output)), nil
}

// loadPlugin loads a .so file and calls its Register function.
// The actual implementation is in platform-specific files (load_unix.go, load_windows.go).
func (pm *Manager) loadPlugin(soPath, pkgPath string) error {
	return pm.loadPluginInternal(soPath, pkgPath)
}

// ListLoaded returns a list of loaded plugin paths.
func (pm *Manager) ListLoaded() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	paths := make([]string, 0, len(pm.loaded))
	for p := range pm.loaded {
		paths = append(paths, p)
	}
	return paths
}

// GetSymbols returns the exported symbols for a package path.
// This is used for tab completion.
func (pm *Manager) GetSymbols(pkgPath string) []string {
	pm.mu.RLock()
	symbols, ok := pm.symbols[pkgPath]
	pm.mu.RUnlock()

	if !ok || symbols == nil {
		return nil
	}

	var result []string
	result = append(result, symbols.Funcs...)
	result = append(result, symbols.Types...)
	result = append(result, symbols.Consts...)
	result = append(result, symbols.Vars...)
	return result
}

// getCurrentTime returns the current Unix timestamp.
func getCurrentTime() int64 {
	return time.Now().Unix()
}
