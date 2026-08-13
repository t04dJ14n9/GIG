// This file produces a human-readable dump of an interpreted
// program's SSA representation. It is intended for inspection and
// teaching, not for runtime execution.
package gig

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/t04dJ14n9/gig/host"
	"github.com/t04dJ14n9/gig/importer"
	"github.com/t04dJ14n9/gig/internal/frontend"
)

// DebugDump compiles sourceCode and returns the readable SSA dump of
// every function in the resulting package. Unlike Build, it does not
// execute init() — only parse, type-check, and SSA construction run.
func DebugDump(sourceCode string, opts ...BuildOption) (string, error) {
	cfg := buildConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.registry == nil {
		cfg.registry = importer.GlobalRegistry()
	}
	env := host.FromRegistry(cfg.registry)
	fcfg := frontend.Config{AutoImport: true}
	if cfg.allowPanic {
		fcfg.Panic = frontend.PanicAllow
	}
	unit, err := frontend.NewBuilder().Build(context.Background(),
		frontend.Source{Content: sourceCode}, env, fcfg)
	if err != nil {
		return "", err
	}
	pkg := unit.Package()
	var b strings.Builder
	fmt.Fprintf(&b, "# Package: %s\n# Path: %s\n\n", pkg.Pkg.Name(), pkg.Pkg.Path())
	keys := make([]string, 0, len(pkg.Members))
	for k := range pkg.Members {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		mem := pkg.Members[name]
		fmt.Fprintf(&b, "# Member %s (%T)\n", name, mem)
		if fn, ok := memberFunc(mem); ok {
			_, _ = fn(&b)
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// memberFunc returns a writer for SSA functions, or false for other
// member kinds. We delegate to the SSA package's WriteTo when present.
func memberFunc(m any) (func(*strings.Builder) (int64, error), bool) {
	// Avoid hard import on go/ssa here; SSA Function exposes WriteTo
	// via *os.File. We adapt to a strings.Builder by capturing through
	// a temporary buffer.
	if fn, ok := m.(interface {
		WriteTo(out io.Writer) (int64, error)
	}); ok {
		return func(b *strings.Builder) (int64, error) {
			return fn.WriteTo(builderWriter{b})
		}, true
	}
	return nil, false
}

// builderWriter adapts strings.Builder to io.Writer via the duck-typed
// interface memberFunc uses.
type builderWriter struct{ b *strings.Builder }

func (w builderWriter) Write(p []byte) (int, error) { return w.b.Write(p) }
