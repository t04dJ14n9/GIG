# SSA Study Lab Design

## Goal

Build a local interactive learning experience that teaches the Go source-to-SSA
pipeline through live examples. A learner edits real Go code and sees the same
source transformed into tokens, AST nodes, type information, control-flow
blocks, and x/tools SSA instructions. Short prediction questions require the
learner to reason before revealing each result.

The first release is for an experienced Go programmer learning compiler
internals. It runs locally from this repository and does not require a Node
toolchain, external service, account, or network connection.

## Why this approach

Three product boundaries were considered:

1. A static interactive book with precomputed output is easy to publish but
   cannot explain arbitrary source edits.
2. A browser-only compiler built with WebAssembly is portable but adds a large
   build/runtime boundary and complicates loading go/types and x/tools.
3. A local Go-backed lab can run the authoritative parser, type checker, and SSA
   builder directly while keeping the frontend small.

The third option is selected. It provides the most faithful mental model and
reuses the language implementation the lessons explain.

## User experience

The application has two modes:

- **Guided lessons**: four ordered lessons introduce token positions, AST
  structure, semantic type information, and SSA/control flow. Each lesson has
  an editable example, a concise explanation, one prediction question, and
  targeted feedback.
- **Free-form lab**: the same synchronized views accept arbitrary single-file
  Go programs so the learner can test hypotheses after finishing the lessons.

The main workspace keeps source visible on the left and one selected pipeline
stage on the right. Selecting a token, AST node, type fact, block, or SSA
instruction highlights the associated source range. The interface never claims
that the stages map one-to-one: constants may produce no instruction, and a
single control-flow construct may produce several blocks and Phi nodes.

Progress and quiz completion are stored in `localStorage`. Source code is sent
only to the local process. There is no telemetry.

## Lesson sequence

### 1. Positions are coordinates

The learner inspects a small function token by token. The exercise asks them to
predict which token owns a selected offset and reinforces that `token.Pos` is a
FileSet-relative integer translated through `token.File` line starts.

### 2. Parsing builds precedence

The source contains `a + b*c`. Before opening the AST result, the learner
predicts the root operator. Selecting `BinaryExpr` nodes reveals the nested
source ranges, showing why multiplication becomes the right child of addition.

### 3. Types add meaning

The learner compares a folded constant expression with arithmetic involving a
parameter. The type view shows expression types, exact constant values, and
Defs/Uses object identity. The exercise predicts which expression has a
non-nil constant value.

### 4. SSA makes control flow explicit

An if/else expression assigns a value that is returned after the join. The
learner predicts the number of successor blocks and where the merged value
comes from, then explores the CFG, `If`, `Jump`, and `Phi` instructions.

## Architecture

### Process boundary

`cmd/ssa-study` is a small HTTP server. It embeds the frontend assets and binds
to `127.0.0.1` only. The command prints its local URL and accepts an optional
`-addr` flag. It does not open a browser automatically.

Endpoints:

- `GET /` and static paths return the embedded application.
- `GET /api/lessons` returns the versioned lesson catalog.
- `POST /api/analyze` accepts `{ "source": "..." }` and returns structured
  analysis. Requests are capped at 64 KiB and use a per-request timeout.

All handlers reject unsupported methods, malformed JSON, oversized input, and
unknown paths with JSON or ordinary HTTP errors as appropriate.

### Analysis package

`internal/study` owns an analyzer independent of HTTP. It runs these phases in
order using one `token.FileSet`:

1. `go/scanner` records lexical tokens and translated source positions.
2. `go/parser` builds an AST with comments and parser recovery enabled.
3. `go/types` populates `types.Info` maps using the standard Go importer.
4. `golang.org/x/tools/go/ssa` builds the package and function CFGs.

The analyzer returns useful earlier-stage data when a later phase fails. A
syntax error can still return tokens and parser diagnostics; a type error can
still return tokens, AST nodes, and type diagnostics. SSA is omitted unless
type checking succeeds.

The response contains:

- tokens with kind, spelling, byte offsets, line, and column;
- a flat, ordered AST tree with stable request-local IDs, parent/depth,
  concrete node kind, source range, and source excerpt;
- semantic facts for expression type/value, Defs, Uses, selections, and generic
  instances, linked to AST node IDs;
- functions, basic blocks, predecessor/successor indices, instructions,
  operands, result names/types, and instruction source positions;
- diagnostics with phase, message, and source coordinates.

The application always treats these IDs as ephemeral. No backend state is
shared between analyses.

### Frontend

The frontend is semantic HTML, CSS, and plain JavaScript under
`cmd/ssa-study/web`, embedded with `go:embed`. Avoiding a package manager keeps
the study tool runnable wherever the Go repository already builds.

Major components are:

- lesson rail and progress indicator;
- editable source panel with line numbers and selected-range overlay;
- four-stage pipeline switcher;
- token ribbon/table;
- expandable AST outline;
- type-fact list grouped by source occurrence;
- SVG CFG whose blocks expose their SSA instruction lists;
- prediction panel with retry/reveal feedback;
- diagnostics area that preserves earlier successful stages.

Input analysis is debounced. Every request carries a monotonically increasing
client sequence so a slower stale response cannot replace newer output.

## Visual direction

The interface should resemble a compiler trace notebook: precise, quiet, and
technical, with the active source span acting like a physical tracing strip
across code and representations. Typography uses a readable sans-serif for
instruction and a true monospace face for source, positions, and SSA. The color
system is neutral blue-gray with one warm signal color for the active mapping;
errors use a separate accessible destructive color. Color is always paired with
labels or selection state.

The memorable interaction is the **trace cursor**: selecting any representation
marks its source range and leaves a thin vertical pipeline trace through the
active stage. Motion is limited to a short block/edge transition and is disabled
under `prefers-reduced-motion`.

The layout is two-column on desktop and stacked on narrow screens. All controls
remain keyboard reachable, selected states use ARIA attributes, the CFG has a
textual alternative, and source/SSA content never relies on color alone.

## Error handling and limits

- The analyzer returns phase-specific diagnostics instead of a single opaque
  failure.
- Panics during analysis are recovered at the HTTP boundary and become a
  generic 500 response; details are logged locally.
- Source is capped at 64 KiB, request bodies use `MaxBytesReader`, and analysis
  has a short context deadline.
- The server listens on loopback by default and sets conservative content-type,
  frame, nosniff, and content-security headers.
- Imported packages use the normal Go importer. The UI explains that the first
  import analysis may take longer and that the lab is intended for small
  single-file examples.

## Testing

### Analyzer tests

- token offsets and line/column translation;
- `a+b*c` AST nesting and source ranges;
- exact constant values plus Defs/Uses identity;
- CFG successors and a Phi-producing function;
- syntax and type errors preserving earlier-stage output;
- request cancellation and source-size behavior where applicable.

### HTTP tests

- embedded index and static asset delivery;
- lesson catalog shape;
- successful analysis response;
- malformed JSON, unsupported methods, oversized body, and unknown route;
- security headers.

### Frontend and end-to-end checks

- JavaScript syntax validation;
- browser smoke flow through all four lessons;
- editing source refreshes every view;
- prediction retry/reveal and progress persistence;
- keyboard operation, narrow viewport reflow, and no console errors.

Repository-wide Go tests must remain green. No existing runtime behavior,
public Gig API, dependency version, or generated code is changed.

## Deliverables

- `internal/study`: structured pipeline analyzer and lesson catalog;
- `cmd/ssa-study`: local server, embedded frontend, and command documentation;
- analyzer and HTTP tests;
- a README section with run instructions;
- browser-verified responsive interactive experience.

## Explicitly deferred

- accounts, cloud persistence, telemetry, and multi-user hosting;
- executing untrusted learner programs;
- multi-file/module editing;
- a full Go language server or autocomplete;
- text annotations/highlights from the Brown book;
- publishing to GitHub Pages or another hosted service;
- complete lessons for every AST node and SSA instruction.

These are future extensions only after the four-lesson lab proves useful.
