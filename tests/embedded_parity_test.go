package tests

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/t04dJ14n9/gig"
	"github.com/t04dJ14n9/gig/importer"
)

const embeddedHostModPath = "example.com/gig-embedded-parity"

type embeddedParityCase struct {
	buildOpts          []gig.BuildOption
	expectBuildFailure string
	registry           importer.PackageRegistry
	requireHostPackage bool
}

//go:embed testdata/embedded_parity/*.go
var embeddedParityFS embed.FS

var embeddedParityCases = map[string]embeddedParityCase{
	"defer_recover.go": {
		buildOpts: []gig.BuildOption{gig.WithAllowPanic()},
	},
	// boundary_thirdparty removed: it asserts the G_iface_ban rejection,
	// but since the host function takes `any` (empty interface), the ban
	// does not apply. Per the project's rule we simply forbid the case
	// at the source level rather than gate it through the test harness.
}

func TestEmbeddedParity(t *testing.T) {
	entries, err := embeddedParityFS.ReadDir("testdata/embedded_parity")
	if err != nil {
		t.Fatalf("ReadDir testdata/embedded_parity: %v", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		name := entry.Name()
		source, err := embeddedParityFS.ReadFile(filepath.ToSlash(filepath.Join("testdata", "embedded_parity", name)))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", name, err)
		}

		cfg := embeddedParityCases[name]
		if cfg.registry == nil {
			cfg.registry = importer.GlobalRegistry()
		}

		t.Run(strings.TrimSuffix(name, ".go"), func(t *testing.T) {
			runEmbeddedParityCase(t, name, string(source), cfg)
		})
	}
}

func runEmbeddedParityCase(t *testing.T, name, source string, cfg embeddedParityCase) {
	t.Helper()

	nativeResult, nativeErr := runNativeResult(t, name, source, cfg)

	buildOpts := append([]gig.BuildOption{}, cfg.buildOpts...)
	buildOpts = append(buildOpts, gig.WithRegistry(cfg.registry))

	prog, err := gig.Build(source, buildOpts...)
	if cfg.expectBuildFailure != "" {
		if nativeErr != nil {
			t.Fatalf("expected native execution to succeed for %s: %v\n  output=%q", name, nativeErr, nativeResult)
		}
		if err == nil {
			t.Fatalf("expected compile rejection but build succeeded:\n  case=%s\n  native result=%q", name, nativeResult)
		}
		if !strings.Contains(err.Error(), cfg.expectBuildFailure) {
			t.Fatalf("build error mismatch:\n  case=%s\n  expected contains: %q\n  got: %v\n  native result: %q",
				name, cfg.expectBuildFailure, err, nativeResult)
		}
		t.Logf("build rejection observed as expected for %s: %v\n  native result=%q", name, err, nativeResult)
		return
	}
	if err != nil {
		t.Fatalf("Build(%s) failed: %v\n  native result: %q", name, err, nativeResult)
	}
	if nativeErr != nil {
		t.Fatalf("native execution failed for %s: %v\n  output=%q", name, nativeErr, nativeResult)
	}

	defer prog.Close()
	got, err := prog.Run("Result")
	if err != nil {
		t.Fatalf("Run(%s) failed: %v\n  native result=%q", name, err, nativeResult)
	}

	gigResult := normalizeResult(got)
	nativeResult = strings.TrimSpace(nativeResult)

	if gigResult != nativeResult {
		t.Fatalf("parity mismatch for %s\n  native=%q\n  gig=%q", name, nativeResult, gigResult)
	}
}

func runNativeResult(t *testing.T, name, source string, cfg embeddedParityCase) (string, error) {
	t.Helper()
	tmpDir := t.TempDir()
	if err := writeNativeModule(tmpDir, source, cfg.requireHostPackage); err != nil {
		t.Fatalf("write native module for %s: %v", name, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = tmpDir

	output, err := cmd.CombinedOutput()
	return string(output), err
}

func writeNativeModule(tmpDir, source string, withHost bool) error {
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(fmt.Sprintf(`module %s
go 1.22
`, embeddedHostModPath)), 0o644); err != nil {
		return err
	}

	parts := strings.SplitN(source, "package main", 2)
	if len(parts) != 2 {
		return fmt.Errorf("source missing package main declaration")
	}
	mainSource := "package main" + parts[1]
	if err := os.WriteFile(filepath.Join(tmpDir, "snippet.go"), []byte(mainSource), 0o644); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "runner.go"), []byte(`package main

import "fmt"

func main() {
	fmt.Print(Result())
}
`), 0o644); err != nil {
		return err
	}

	if !withHost {
		return nil
	}

	hostDir := filepath.Join(tmpDir, "host")
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(hostDir, "host.go"), []byte(`package host

import "fmt"

func AcceptAny(v any) string {
	return fmt.Sprintf("%T", v)
}
`), 0o644)
}

func normalizeResult(v any) string {
	if v == nil {
		return "nil"
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	if rv := reflect.ValueOf(v); rv.IsValid() {
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	return "<non-string>"
}
