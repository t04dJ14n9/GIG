# AST and SSA Reference Manual Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create an exhaustive, version-pinned manual for every exported Go
1.26.3 `ast.Node` implementation and every x/tools v0.30.0
`ssa.Instruction`, including AST-to-SSA lowering and Gig interpreter links.

**Architecture:** Add one searchable Markdown reference beside the existing
pipeline report. Use one row or dedicated entry per concrete type, small
Mermaid diagrams for interface relationships, and source-derived inventory
checks to prove completeness.

**Tech Stack:** Markdown, Mermaid, Go 1.26.3 `go/ast`,
`golang.org/x/tools/go/ssa` v0.30.0, Gig's Go test suites.

## Global Constraints

- AST definitions and official links target Go 1.26.3.
- SSA definitions and official links target x/tools v0.30.0.
- Create `docs/AST_SSA_REFERENCE.md`; do not replace
  `docs/SSA_PIPELINE.md`.
- Do not modify runtime code, APIs, dependencies, generated files, or build
  configuration.
- Include every one of the 57 exported concrete Go 1.26.3 AST node types and
  every one of the 37 x/tools v0.30.0 SSA instruction types.
- Document SSA value-only nodes and support structures separately without
  labeling them instructions.
- Do not stage or commit files unless the user explicitly requests it.

---

### Task 1: Establish the reference skeleton and exact inventories

**Files:**

- Create: `docs/AST_SSA_REFERENCE.md`
- Reference: `docs/SSA_PIPELINE.md`
- Reference: `docs/superpowers/specs/2026-07-17-ast-ssa-reference-manual-design.md`

**Interfaces:**

- Consumes: Go 1.26.3 `go/ast` exported method sets and x/tools v0.30.0
  `ssa.Instruction` method sets.
- Produces: stable headings and one checklist row for every concrete node and
  instruction, used by all later tasks.

- [ ] **Step 1: Record the version contract and reading conventions**

Create the document with these top-level sections:

```markdown
# Go AST and x/tools SSA Reference Manual

## Version and scope
## 1. How to read Go AST nodes
## 2. Complete Go 1.26.3 AST catalog
## 3. How to read x/tools SSA
## 4. Complete x/tools v0.30.0 SSA instruction catalog
## 5. AST-to-SSA lowering guide
## 6. Gig integration and instruction support
## 7. Completeness inventories
```

State clearly that `go/ast` and `x/tools/go/ssa` are source-analysis
representations and that the SSA package is not
`cmd/compile/internal/ssa`.

- [ ] **Step 2: Add the complete AST inventory**

The catalog must contain each of these names exactly once as a catalog entry:

```text
ArrayType AssignStmt BadDecl BadExpr BadStmt BasicLit BinaryExpr BlockStmt
BranchStmt CallExpr CaseClause ChanType CommClause Comment CommentGroup
CompositeLit DeclStmt DeferStmt Directive Ellipsis EmptyStmt ExprStmt Field
FieldList File ForStmt FuncDecl FuncLit FuncType GenDecl GoStmt Ident IfStmt
ImportSpec IncDecStmt IndexExpr IndexListExpr InterfaceType KeyValueExpr
LabeledStmt MapType Package ParenExpr RangeStmt ReturnStmt SelectStmt
SelectorExpr SendStmt SliceExpr StarExpr StructType SwitchStmt TypeAssertExpr
TypeSpec TypeSwitchStmt UnaryExpr ValueSpec
```

Expected count: `57`.

- [ ] **Step 3: Add the complete SSA instruction inventory**

The catalog must contain each of these names exactly once as an instruction
entry:

```text
Alloc BinOp Call ChangeInterface ChangeType Convert DebugRef Defer Extract
Field FieldAddr Go If Index IndexAddr Jump Lookup MakeChan MakeClosure
MakeInterface MakeMap MakeSlice MapUpdate MultiConvert Next Panic Phi Range
Return RunDefers Select Send Slice SliceToArrayPointer Store TypeAssert UnOp
```

Expected count: `37`.

- [ ] **Step 4: Add the taxonomy diagrams**

Add one Mermaid diagram showing:

```text
ast.Node -> Expr, Stmt, Decl, Spec, auxiliary nodes
```

Add one Mermaid diagram showing the overlap:

```text
ssa.Node -> ssa.Value
ssa.Node -> ssa.Instruction
value-producing instructions implement both
```

- [ ] **Step 5: Verify the initial structure**

Run:

```bash
rg -n '^#|^```mermaid' docs/AST_SSA_REFERENCE.md
```

Expected: all seven numbered sections and two Mermaid fences are present.

---

### Task 2: Write the complete AST catalog

**Files:**

- Modify: `docs/AST_SSA_REFERENCE.md`
- Reference: `$(go env GOROOT)/src/go/ast/ast.go`
- Reference: `$(go env GOROOT)/src/go/ast/directive.go`

**Interfaces:**

- Consumes: the 57-name inventory from Task 1.
- Produces: one explanatory catalog entry per node, plus the semantic context
  needed by the lowering guide.

- [ ] **Step 1: Explain AST interfaces and position behavior**

Document `Node.Pos`, `Node.End`, the marker interfaces `Expr`, `Stmt`,
`Decl`, and `Spec`, optional nil fields, and the distinction between syntax
identity and `go/types` object identity.

- [ ] **Step 2: Document auxiliary and file-level nodes**

Cover exactly:

```text
Comment CommentGroup Directive Field FieldList File Package
```

For each entry include important fields, a minimal source example or role,
position behavior, and an official Go 1.26.3 source link.

- [ ] **Step 3: Document all expression and type-expression nodes**

Cover exactly:

```text
BadExpr Ident Ellipsis BasicLit FuncLit CompositeLit ParenExpr SelectorExpr
IndexExpr IndexListExpr SliceExpr TypeAssertExpr CallExpr StarExpr UnaryExpr
BinaryExpr KeyValueExpr ArrayType StructType FuncType InterfaceType MapType
ChanType
```

Explain that type syntax also implements `ast.Expr`, and identify
`types.Info.Types`, `Uses`, `Selections`, or `Instances` where each is
semantically important.

- [ ] **Step 4: Document all statement nodes**

Cover exactly:

```text
BadStmt DeclStmt EmptyStmt LabeledStmt ExprStmt SendStmt IncDecStmt AssignStmt
GoStmt DeferStmt ReturnStmt BranchStmt BlockStmt IfStmt CaseClause SwitchStmt
TypeSwitchStmt CommClause SelectStmt ForStmt RangeStmt
```

Explain optional components such as `IfStmt.Init`, `IfStmt.Else`,
`ForStmt.Cond`, and `RangeStmt.Key/Value`.

- [ ] **Step 5: Document all specification and declaration nodes**

Cover exactly:

```text
ImportSpec ValueSpec TypeSpec BadDecl GenDecl FuncDecl
```

Explain how `GenDecl.Tok` selects import/const/type/var meaning and how
`types.Info.Defs` associates declaration identifiers with objects.

- [ ] **Step 6: Check AST catalog coverage**

Extract the authoritative list:

```bash
go doc -all go/ast |
sed -nE 's/^func \([^)]*\*([A-Za-z0-9_]+)\) (Pos|End)\(\).*/\1 \2/p' |
sort -u |
awk '{ seen[$1]++ } END { for (name in seen) if (seen[name] == 2) print name }' |
sort
```

Compare it with the manual inventory. Expected: `57` authoritative names,
with no missing or extra catalog entries.

---

### Task 3: Write the complete SSA catalog

**Files:**

- Modify: `docs/AST_SSA_REFERENCE.md`
- Reference: `$(go env GOMODCACHE)/golang.org/x/tools@v0.30.0/go/ssa/ssa.go`

**Interfaces:**

- Consumes: the 37-name instruction inventory from Task 1.
- Produces: complete instruction semantics and the terminology used by the
  lowering and Gig support sections.

- [ ] **Step 1: Explain SSA containers and interfaces**

Document `Program`, `Package`, `Function`, `BasicBlock`, `Node`,
`Value`, `Instruction`, `Member`, and `CallInstruction`. Explain block
ownership, operands, referrers, tuple values, predecessor/successor edges, and
terminators.

- [ ] **Step 2: Document value-only nodes and support structures**

Cover `Const`, `Global`, `Builtin`, `Function`, `Parameter`, and
`FreeVar` as non-instruction values. Cover `CallCommon` and `SelectState`
as instruction-support structures.

- [ ] **Step 3: Document all 26 value-producing instructions**

Cover exactly:

```text
Alloc Phi Call BinOp UnOp ChangeType Convert MultiConvert ChangeInterface
SliceToArrayPointer MakeInterface MakeClosure MakeMap MakeChan MakeSlice Slice
FieldAddr Field IndexAddr Index Lookup Select Range Next TypeAssert Extract
```

For each entry state operands, result type, side effects, source constructs,
and invariants.

- [ ] **Step 4: Document all 11 effect-only instructions**

Cover exactly:

```text
Jump If Return RunDefers Panic Go Defer Send Store MapUpdate DebugRef
```

For each entry state operands, control-flow behavior, side effects, and source
constructs.

- [ ] **Step 5: Check SSA instruction coverage**

Run:

```bash
go doc -all golang.org/x/tools/go/ssa |
sed -nE 's/^func \([^)]*\*([A-Za-z0-9_]+)\) Block\(\).*/\1/p' |
sort -u
```

Expected: `37` names, with no missing or extra instruction entries.

---

### Task 4: Connect AST, types.Info, SSA, and Gig

**Files:**

- Modify: `docs/AST_SSA_REFERENCE.md`
- Reference: `internal/frontend/builder.go:45-130`
- Reference: `debug_dump.go:19-58`
- Reference: `internal/interp/plan.go`
- Reference: `internal/interp/frame.go`
- Reference: `internal/interp/ops.go`

**Interfaces:**

- Consumes: AST and SSA terminology from Tasks 2 and 3.
- Produces: the lowering matrix and current Gig support map.

- [ ] **Step 1: Add the semantic lowering diagram**

Show:

```text
AST node + types.Info -> SSA builder decision -> zero or more SSA instructions
```

Make clear that constant folding can produce no runtime instruction and that
one AST node may produce multiple blocks or instructions.

- [ ] **Step 2: Add the AST-to-SSA lowering matrix**

Include representative mappings for literals, identifiers, unary/binary
expressions, calls/conversions, selectors, indexing, slicing, composite
literals, assignments, branches, loops, switches, select, go/defer, return,
and function literals.

- [ ] **Step 3: Add the Gig pipeline links**

Link the shared `FileSet`, parser invocation, `types.Info` construction,
`ssa.NewProgram`, `CreatePackage`, `Build`, `DebugDump`, and interpreter
instruction dispatch.

- [ ] **Step 4: Add the Gig instruction support map**

Classify each SSA instruction as directly handled, ignored metadata,
optimized plus generic fallback, or unsupported/error-producing. Base every
classification on the current interpreter type switch and operation runners;
do not infer support from instruction names.

- [ ] **Step 5: Review the support map against source**

Use CodeGraph on `internal/interp/plan.go`, `frame.go`, and `ops.go`.
Expected: every support claim cites or names the current handler/fallback path.

---

### Task 5: Verify and hand off the manual

**Files:**

- Verify: `docs/AST_SSA_REFERENCE.md`
- Verify: `docs/superpowers/specs/2026-07-17-ast-ssa-reference-manual-design.md`
- Verify: `docs/superpowers/plans/2026-07-17-ast-ssa-reference-manual.md`

**Interfaces:**

- Consumes: the complete manual.
- Produces: fresh evidence that inventories, links, Markdown, and repository
  behavior are valid.

- [ ] **Step 1: Check Markdown structure**

Check balanced fences, no trailing whitespace, and the required headings.
Expected: zero structural errors.

- [ ] **Step 2: Validate local links and anchors**

Extract repository-relative Markdown targets, resolve them from `docs/`, and
check line anchors against target line counts. Expected: zero missing files or
invalid anchors.

- [ ] **Step 3: Validate external links**

Send HTTP requests to each unique official version-pinned URL. Expected: every
URL returns a 2xx or 3xx status.

- [ ] **Step 4: Re-run both inventory comparisons**

Expected:

```text
AST nodes:        57 authoritative, 57 documented, no difference
SSA instructions: 37 authoritative, 37 documented, no difference
```

- [ ] **Step 5: Run repository tests**

Run:

```bash
go test ./...
(cd cmd/gig && go test ./...)
```

Expected: both commands exit successfully with no failing package.

- [ ] **Step 6: Review the final worktree scope**

Run:

```bash
git status --short -- docs/AST_SSA_REFERENCE.md \
  docs/superpowers/specs/2026-07-17-ast-ssa-reference-manual-design.md \
  docs/superpowers/plans/2026-07-17-ast-ssa-reference-manual.md
```

Expected: only the requested manual and its planning documents are new or
modified by this task. Do not stage or commit them without explicit user
authorization.
