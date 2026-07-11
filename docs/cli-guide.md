# Gig CLI Guide

The `gig` command-line tool generates dependency registration packages and
prints readable SSA for interpreted programs. It provides three commands:
`init`, `gen`, and `dump`.

## Installation

Gig requires Go 1.23.1 or later.

```bash
go install github.com/t04dJ14n9/gig/cmd/gig@latest
```

## Dependency Package Workflow

Gig interprets Go code without dynamically importing arbitrary packages.
Applications register the packages their interpreted programs may use.

### `gig init`

Create a dependency package directory containing a `pkgs.go` template:

```bash
gig init -package mydep
```

The package name is required and must be a valid Go identifier. The command
creates:

```text
mydep/
└── pkgs.go
```

Edit `mydep/pkgs.go` and add blank imports for the packages to expose:

```go
package mydep

import (
	_ "fmt"
	_ "strings"
	_ "github.com/spf13/cast"
)
```

### `gig gen`

Generate registration wrappers from the imports in `<dir>/pkgs.go`:

```bash
gig gen ./mydep
```

Generated files are written to `<dir>/packages/`:

```text
mydep/
├── pkgs.go
└── packages/
    ├── fmt.go
    ├── strings.go
    └── github_com_spf13_cast.go
```

Import the generated package for its registration side effects:

```go
import _ "your/module/mydep/packages"
```

The generated wrappers register exported functions, constants, variables, and
types with Gig.

## Inspecting Interpreted Programs

### `gig dump`

Compile Gig source and print its readable SSA representation:

```bash
gig dump program.go
```

Use `-` to read source from standard input:

```bash
printf 'package main; func Add(a, b int) int { return a + b }' | gig dump -
```

Use `--raw` for inline source:

```bash
gig dump --raw 'package main; func main() {}'
```

The optional `--allow-panic` flag permits panic, recover, and defer while
building the debug dump:

```bash
gig dump --allow-panic program.go
```

`--raw` cannot be combined with a file argument. Without `--raw`, exactly one
file path or `-` is required.

## Troubleshooting

### `pkgs.go not found`

Initialize the dependency package before generating wrappers:

```bash
gig init -package mydep
gig gen ./mydep
```

### `no imports found`

Add at least one blank import to `<dir>/pkgs.go`, then rerun `gig gen`.

### A package cannot be generated

Confirm the package is available to the current module and toolchain:

```bash
go get <package-path>
go list <package-path>
```

Then rerun `gig gen`.

## See Also

- [README.md](../README.md) — library overview and examples
- [ARCHITECTURE.md](ARCHITECTURE.md) — internal architecture
- [examples/](../examples/) — example programs
