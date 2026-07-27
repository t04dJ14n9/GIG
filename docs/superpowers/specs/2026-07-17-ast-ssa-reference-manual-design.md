# AST and SSA Reference Manual Design

## Goal

Create an exhaustive, educational reference manual that documents every
concrete Go 1.26.3 `go/ast` node and every instruction in
`golang.org/x/tools/go/ssa` v0.30.0, then connects those two representations
to Gig's frontend and interpreter.

## Deliverable

Create `docs/AST_SSA_REFERENCE.md` as a companion to
`docs/SSA_PIPELINE.md`. The existing pipeline report remains the conceptual
walkthrough; the new document is the exhaustive type-by-type field guide.

No runtime code, public API, generated code, dependency, or build configuration
will change.

## Version scope

- AST definitions and links: Go 1.26.3.
- SSA definitions and links: `golang.org/x/tools/go/ssa` v0.30.0, matching
  Gig's `go.mod`.
- Gig links and support notes: the current `codex/simplification` worktree.
- Version differences that materially affect the inventory are called out
  explicitly; the manual does not attempt to catalog every historical Go or
  x/tools release.

## Organization

The manual is one integrated document with five parts:

1. **Reading model** — the `ast.Node`, `ast.Expr`, `ast.Stmt`,
   `ast.Decl`, and `ast.Spec` interfaces; source-position conventions; and
   the SSA `Node`, `Value`, and `Instruction` interfaces.
2. **Complete AST catalog** — comments and files, fields, expressions and type
   expressions, statements, specifications, and declarations.
3. **Complete SSA catalog** — value-producing and effect-only instructions,
   grouped by control flow, memory, operators, conversion, aggregate access,
   calls, concurrency, maps/channels/slices, type operations, and debugging.
4. **Lowering guide** — mappings from representative AST nodes, informed by
   `types.Info`, to the SSA instructions that may result.
5. **Gig integration** — links to frontend construction, readable dumps, and
   interpreter handling, including explicit supported/unsupported notes.

Static Mermaid diagrams will show the AST interface taxonomy, SSA interface
overlap, and the AST-plus-`types.Info` to SSA lowering relationship. Tables
will carry the exhaustive inventories because they are more searchable and
compact than one diagram node per concrete type.

## Entry format

Each AST entry includes:

- concrete type and category;
- source construct represented;
- important fields and their meaning;
- a minimal Go example;
- relevant position behavior or structural invariant;
- the `types.Info` maps commonly associated with it;
- an official Go 1.26.3 source link.

Each SSA instruction entry includes:

- concrete instruction type;
- whether it also implements `ssa.Value`;
- operand and result shape;
- side effects and control-flow behavior;
- source constructs that can produce it;
- important invariants, including tuple and predecessor indexing;
- an official x/tools v0.30.0 source link;
- Gig interpreter handling where applicable.

Closely related trivial entries may share an example, but no concrete node or
instruction may be omitted from the inventory.

## Completeness definition

The AST inventory is complete when every exported concrete struct in the Go
1.26.3 `go/ast` package that implements `ast.Node` appears exactly once in a
catalog or explicit auxiliary-node section. This includes nodes defined outside
`ast.go`, such as `Directive`.

The SSA inventory is complete when every concrete type in x/tools v0.30.0 that
implements `ssa.Instruction` appears exactly once. Value-only SSA nodes and
instruction-support structures such as `CallCommon` and `SelectState` are
documented separately so that instruction entries are understandable without
claiming those helpers are instructions.

## Source links

Links use immutable version tags:

- `https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go`
- `https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go`
- other x/tools SSA implementation files at the same `v0.30.0` tag

Repository-relative links point to the current Gig frontend, debug dump, and
interpreter files, with line anchors when stable and useful.

## Verification

Before handoff:

1. Extract the AST and SSA concrete-type inventories from their pinned source
   versions and compare them with the documented names.
2. Confirm every repository-relative link resolves and every local line anchor
   is within the target file.
3. Confirm all external version-pinned links return a successful HTTP status.
4. Check Markdown fences, Mermaid fence pairing, and trailing whitespace.
5. Run `go test ./...` in both the root module and `cmd/gig` module.
6. Review the Gig support table against the current interpreter dispatch and
   operation implementations.

## Non-goals

- Documenting compiler-backend `cmd/compile/internal/ssa`.
- Replacing the existing end-to-end SSA pipeline report.
- Teaching the full Go grammar or type system independently of AST/SSA use.
- Changing Gig to support an instruction it does not currently implement.
- Generating public documentation or code from the manual.
