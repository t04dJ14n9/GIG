# Interactive SSA Study Lab

The SSA Study Lab is a local interactive companion to the source-to-SSA
documentation. It lets you change a real Go file and inspect each
representation produced by Go 1.26.3 and `golang.org/x/tools/go/ssa` v0.30.0.

## Start the lab

From the repository root:

```bash
go run ./cmd/ssa-study
```

Open <http://127.0.0.1:8080>. To use a different loopback port:

```bash
go run ./cmd/ssa-study -addr 127.0.0.1:18080
```

The command embeds the application, so no Node installation, package manager,
asset build, or network connection is required.

## Learning sequence

The four lessons deliberately follow the compiler pipeline:

1. **Positions are coordinates** uses scanner tokens to connect byte offsets,
   `token.Pos`, line starts, and translated line/column positions.
2. **Parsing builds precedence** makes the nested `BinaryExpr` structure of
   `a + b*c` visible and traceable to source.
3. **Types add meaning** compares AST spellings with `types.Info` expression
   types, exact constants, definitions, uses, and object identity.
4. **SSA makes flow explicit** turns an if/else into basic blocks, successor
   edges, `If`, `Jump`, `Phi`, and `Return` instructions.

Each lesson asks for a prediction. An incorrect prediction can be retried
without revealing the answer. Choosing **Show explanation** reveals and locks
the checkpoint until you reset that lesson. Correct lessons and attempt counts
are stored locally in the browser under `ssa-study-progress-v1`.

## Using the workspace

- Edit `study.go` in the left pane. Analysis runs after a short pause.
- Switch between **Tokens**, **AST**, **Types**, and **SSA + CFG**.
- Select an item in any representation to highlight its associated source
  range. Generated SSA instructions without an exact range are identified as
  such.
- In the AST view, collapse a node to hide all of its descendants.
- In the SSA view, choose a function, follow the edge map, inspect individual
  instructions, or expand the raw x/tools SSA dump.
- Choose **Open free lab** to keep the current source and explore without a
  graded checkpoint.

Diagnostics are phase-specific. Syntax-invalid source can still show scanner
tokens and a recovered AST. Type-invalid source keeps those earlier views but
omits SSA because SSA construction requires a successfully checked package.

## Safety and limits

- The server listens on `127.0.0.1` by default.
- Source is sent only to that local process. There is no telemetry or account.
- The analyzer scans, parses, type-checks, and builds SSA; it never executes
  learner code.
- A request accepts one complete Go file up to 64 KiB.
- Imports use the installed Go toolchain's standard importer. The first analysis
  of an imported package may take longer.
- Analysis requests have a five-second deadline.

The tool is designed for small experiments, not multi-file module editing or
untrusted public hosting.

## Continue reading

- [From Go Source to SSA](SSA_PIPELINE.md) explains how `go/token`,
  `go/parser`, `go/types`, and x/tools SSA cooperate.
- [Go AST and x/tools SSA Reference Manual](AST_SSA_REFERENCE.md) catalogs all
  public concrete AST nodes and SSA instructions used by the selected versions.

The lab uses the same underlying standard-library and x/tools APIs described by
those manuals; the browser views are structured presentations of live analysis,
not precomputed diagrams.
