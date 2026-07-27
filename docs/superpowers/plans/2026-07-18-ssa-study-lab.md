# SSA Study Lab Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a dependency-free local web application that lets a learner edit Go source and interactively inspect its tokens, AST, type information, CFG, and x/tools SSA while completing four guided prediction lessons.

**Architecture:** Add a structured stateless analyzer in `internal/study`, then expose it and a lesson catalog through a loopback-only HTTP command in `cmd/ssa-study`. Embed a semantic HTML/CSS/JavaScript frontend that synchronizes selections through source byte ranges and stores lesson progress in `localStorage`.

**Tech Stack:** Go 1.26.3 standard library, `golang.org/x/tools/go/ssa` v0.30.0, embedded static assets, plain ES modules, SVG, Go tests, Node syntax checks, Playwright browser smoke checks.

## Global Constraints

- Do not add dependencies or modify either `go.mod`.
- Do not execute learner source; only scan, parse, type-check, and build SSA.
- Accept one complete Go source file up to 64 KiB per analysis.
- Use one `token.FileSet` for parser, type checker, and SSA positions.
- Preserve successful earlier-stage data when parsing or type checking reports errors.
- Bind to `127.0.0.1:8080` by default and use a 5-second analysis timeout.
- Keep frontend functionality usable without network access or a package manager.
- Do not modify Gig runtime/interpreter behavior or public APIs.
- Preserve unrelated dirty-worktree changes and stage only task-owned files.

---

### Task 1: Structured lexical and AST analysis

**Files:**

- Create: `internal/study/model.go`
- Create: `internal/study/analyze.go`
- Create: `internal/study/ast.go`
- Create: `internal/study/analyze_test.go`

**Interfaces:**

- Produces: `func Analyze(ctx context.Context, source string) Result`.
- Produces: JSON-safe `Result`, `SourceRange`, `Token`, `ASTNode`, and `Diagnostic`.

- [ ] **Step 1: Define contracts and failing token/AST tests**

```go
type Result struct {
    Source string `json:"source"`
    Tokens []Token `json:"tokens"`
    AST []ASTNode `json:"ast"`
    Types []TypeFact `json:"types"`
    Functions []Function `json:"functions"`
    Diagnostics []Diagnostic `json:"diagnostics"`
}
type SourceRange struct { Start, End, Line, Column, EndLine, EndColumn int }
type Token struct { Kind, Text string; Range SourceRange }
type ASTNode struct { ID, Parent, Depth int; Kind, Excerpt string; Range SourceRange }
type Diagnostic struct { Phase, Message string; Line, Column int }
```

Add `TestAnalyzeTokensUseParserFilePositions`, which asserts the `func`
token's byte/line/column and that every token range safely slices the source.
Add `TestAnalyzeASTRespectsBinaryPrecedence`, which finds the outer
`a + b*c` BinaryExpr and its nested `b*c` child.

- [ ] **Step 2: Verify the tests fail**

Run: `go test ./internal/study -run 'TestAnalyze(Tokens|AST)'`

Expected: compilation fails because the analyzer API is missing.

- [ ] **Step 3: Implement scanning, parsing, and AST extraction**

Create one FileSet and parse with
`parser.AllErrors|parser.ParseComments|parser.SkipObjectResolution`. Locate
the parser-created `token.File`, initialize `scanner.Scanner` on that file,
and convert positions through `token.File.Offset`. Use `ast.Inspect` nil
callbacks to maintain parent/depth stacks. Clamp source excerpts and translate
scanner/parser errors into phase-tagged diagnostics. Check `ctx.Err()` between
phases and keep partial AST data after recoverable parse errors.

- [ ] **Step 4: Verify focused tests pass**

Run: `go test ./internal/study -run 'TestAnalyze(Tokens|AST)' -v`

Expected: both tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/study
git commit -m "feat(study): expose tokens and AST structure"
```

---

### Task 2: Type facts, CFG, and SSA instructions

**Files:**

- Modify: `internal/study/model.go`
- Modify: `internal/study/analyze.go`
- Create: `internal/study/ssa.go`
- Modify: `internal/study/analyze_test.go`

**Interfaces:**

- Consumes: AST IDs and ranges from Task 1.
- Produces: `TypeFact`, `Function`, `BasicBlock`, and `Instruction`.

- [ ] **Step 1: Add contracts and failing semantic tests**

```go
type TypeFact struct { ASTID int; Kind, Name, Type, Value, Object string; Range SourceRange }
type Function struct { Name, Signature, Raw string; Blocks []BasicBlock }
type BasicBlock struct { Index int; Comment string; Preds, Succs []int; Instructions []Instruction }
type Instruction struct { Kind, Text, Result, Type string; Operands []string; ASTID int; Range SourceRange }
```

Test exact value `5` for `2+3`, shared Def/Use object identity, an `If`
and `Phi` in a branch-join function, token retention after syntax errors, and
AST retention with no SSA after type errors.

- [ ] **Step 2: Verify semantic tests fail**

Run: `go test ./internal/study -run 'TestAnalyze(Types|SSA|Errors)' -v`

Expected: failures because Types and Functions are empty.

- [ ] **Step 3: Populate go/types facts**

Initialize all `types.Info` maps for Types, Defs, Uses, Selections, Scopes, and
Instances. Check with `importer.Default()` and 64-bit `types.StdSizes`.
Convert callback errors to diagnostics. Link facts to AST IDs, include exact
constant values and object strings, sort by source offset/kind, and stop before
SSA if checking fails.

- [ ] **Step 4: Build structured SSA**

Use `ssa.SanityCheckFunctions`, create type-only imported packages, then
create/build the source package. Collect package and anonymous functions in
deterministic order. Record block indices/comments/edges and instruction kind,
text, result, type, operands, source position, and nearest containing AST ID.
Include `Function.WriteTo` output in `Raw`.

- [ ] **Step 5: Verify and commit**

Run: `go test ./internal/study -v`

Expected: all analyzer tests pass.

```bash
git add internal/study
git commit -m "feat(study): expose type and SSA analysis"
```

---

### Task 3: Lessons and loopback HTTP server

**Files:**

- Create: `internal/study/lessons.go`
- Create: `internal/study/lessons_test.go`
- Create: `cmd/ssa-study/main.go`
- Create: `cmd/ssa-study/server.go`
- Create: `cmd/ssa-study/server_test.go`
- Create: `cmd/ssa-study/web/index.html`

**Interfaces:**

- Produces: `func Lessons() []Lesson` with four immutable-by-copy records.
- Produces: `func newHandler() http.Handler`.
- Consumes: `study.Analyze(ctx, source)` at `POST /api/analyze`.

- [ ] **Step 1: Add failing catalog/handler tests**

Define Lesson fields ID, Number, Title, Kicker, Explanation, Stage, Source, and
a Quiz containing Prompt, Options, Answer, Correct, and Incorrect. Assert IDs
`positions`, `precedence`, `types`, and `control-flow`, complete Go
sources, and valid answers. Test index, lessons, analysis, method rejection,
malformed JSON, 64-KiB overflow, unknown route, and security headers.

- [ ] **Step 2: Verify tests fail**

Run: `go test ./internal/study ./cmd/ssa-study -run 'Test(Lessons|Handler)' -v`

Expected: compilation fails because catalog/server APIs are missing.

- [ ] **Step 3: Implement catalog and server**

Return deep-copied lessons. Embed `web/*`; use `http.MaxBytesReader`,
`json.Decoder.DisallowUnknownFields`, and a 5-second context timeout. Apply
CSP `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:`,
nosniff, and frame-denial headers. Recover panics at middleware. Parse `-addr`
in main, default to `127.0.0.1:8080`, print the URL, and call ListenAndServe.

- [ ] **Step 4: Verify and commit**

Run: `go test ./internal/study ./cmd/ssa-study -v`

Expected: all tests pass.

```bash
git add internal/study/lessons.go internal/study/lessons_test.go cmd/ssa-study
git commit -m "feat(study): serve lessons and live analysis"
```

---

### Task 4: Interactive token, AST, and type views

**Files:**

- Modify: `cmd/ssa-study/web/index.html`
- Create: `cmd/ssa-study/web/styles.css`
- Create: `cmd/ssa-study/web/app.js`
- Create: `cmd/ssa-study/web/renderers.js`

**Interfaces:**

- Consumes: `/api/lessons` and `/api/analyze`.
- Produces: stage selection plus shared `selectRange(range, astID)`.

- [ ] **Step 1: Build the semantic shell**

Add skip link, masthead, lesson rail, source editor with mirrored highlight and
line gutter, stage tablist, output, quiz, diagnostics, and free-lab button.
Include only local CSS and ES modules.

- [ ] **Step 2: Apply the compiler-trace visual system**

Use cool-paper, ink, blueprint, signal-orange, and destructive CSS variables;
system sans/monospace; a 260px rail plus fluid workspace; a stacked layout
below 820px; a signal-colored trace cursor; visible focus; reduced-motion; and
44px minimum interactive targets.

- [ ] **Step 3: Implement state and live analysis**

Load lessons and `ssa-study-progress-v1`, select the first incomplete lesson,
debounce editor input by 280ms, sequence requests, and ignore stale responses.
Retain earlier-stage output and show diagnostics when later phases fail.

- [ ] **Step 4: Implement three renderers**

Render selectable token rows with spelling/kind/range; AST preorder rows with
depth/disclosure; and type facts with excerpt/type/value/object. Each selection
calls `selectRange` and maintains `aria-selected`.

- [ ] **Step 5: Verify and commit**

Run: `node --check cmd/ssa-study/web/app.js`

Run: `node --check cmd/ssa-study/web/renderers.js`

Run: `go test ./cmd/ssa-study -v`

Expected: all commands exit zero.

```bash
git add cmd/ssa-study/web
git commit -m "feat(study): add interactive pipeline views"
```

---

### Task 5: CFG, quizzes, and progress

**Files:**

- Modify: `cmd/ssa-study/web/app.js`
- Modify: `cmd/ssa-study/web/renderers.js`
- Modify: `cmd/ssa-study/web/styles.css`

**Interfaces:**

- Consumes: Function/BasicBlock/Instruction records and Lesson.Quiz.
- Produces: keyboard-selectable CFG/instructions and versioned progress.

- [ ] **Step 1: Render function selection and CFG**

Default to the first non-init function. Lay blocks in deterministic rows, draw
SVG successor edges, expose block metadata and instructions, map selections to
source, and add a textual screen-reader edge/block summary.

- [ ] **Step 2: Implement prediction checks**

Use native radios. “Check prediction” permits retry without revealing the
answer; “Show explanation” reveals and locks the quiz. Success marks completion
and moves focus to feedback.

- [ ] **Step 3: Persist progress and add free lab**

Store `{version:1, completed:[ids], attempts:{id:number}}` at
`ssa-study-progress-v1`. Free lab preserves source and removes grading.
Resetting a lesson restores only that lesson and clears its progress.

- [ ] **Step 4: Verify and commit**

Run: `node --check cmd/ssa-study/web/app.js`

Run: `node --check cmd/ssa-study/web/renderers.js`

Run: `go test ./internal/study ./cmd/ssa-study -v`

Expected: all commands exit zero.

```bash
git add cmd/ssa-study/web
git commit -m "feat(study): add CFG lessons and progress"
```

---

### Task 6: Documentation and end-to-end verification

**Files:**

- Modify: `README.md`
- Create: `docs/SSA_STUDY_LAB.md`
- Verify: all Task 1–5 files.

**Interfaces:**

- Produces: exact run instructions and fresh completion evidence.

- [ ] **Step 1: Document the lab**

Add `go run ./cmd/ssa-study` and the default URL to README. Document lessons,
free lab, local-only privacy, source limit, non-execution, and links to
`docs/SSA_PIPELINE.md` and `docs/AST_SSA_REFERENCE.md`.

- [ ] **Step 2: Run full static and Go verification**

```bash
gofmt -w internal/study cmd/ssa-study/*.go
go test ./internal/study ./cmd/ssa-study
go test ./...
(cd cmd/gig && go test ./...)
node --check cmd/ssa-study/web/app.js
node --check cmd/ssa-study/web/renderers.js
```

Expected: every command exits zero.

- [ ] **Step 3: Exercise the application**

Start at `127.0.0.1:18080`. Verify index, lessons, and analysis return 200.
In a browser, complete one quiz, edit precedence source, select AST and SSA
items, switch to free lab, reload progress, and repeat at 390px width. Confirm
no console errors and capture desktop/mobile screenshots.

- [ ] **Step 4: Check accessibility and failures**

Use keyboard-only navigation. Submit syntax-invalid and type-invalid source and
confirm earlier phases remain visible with diagnostics. Confirm reduced motion
disables transitions and the CFG has an accessible description.

- [ ] **Step 5: Verify scope and commit**

Run: `git status --short`

Run: `git diff --check HEAD~5 -- internal/study cmd/ssa-study README.md docs/SSA_STUDY_LAB.md`

Expected: no whitespace errors and no unrelated task files staged.

```bash
git add README.md docs/SSA_STUDY_LAB.md internal/study cmd/ssa-study
git commit -m "docs: explain the SSA study lab"
```
