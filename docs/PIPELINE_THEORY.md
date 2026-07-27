# From Bytes to CFG: The Theory Behind Gig's Frontend Pipeline

This document explains **how** each stage of the pipeline works internally:
how the scanner turns bytes into tokens, how top-down LL(1) parsing works and
how `go/parser` embodies (and bends) it, what the `go/types` checker passes
actually do, and how `x/tools/go/ssa` builds SSA form and a control-flow
graph out of a typed syntax tree.

It is the theory companion to three reference documents:

- [`SSA_PIPELINE.md`](SSA_PIPELINE.md) — stage-by-stage tour of the same
  pipeline with API detail and Gig code pointers.
- [`GO_TYPES_CHECKER_PASSES.md`](GO_TYPES_CHECKER_PASSES.md) — every
  `go/types` pass, one by one, with checker state.
- [`AST_SSA_REFERENCE.md`](AST_SSA_REFERENCE.md) — complete AST node and SSA
  instruction catalogs.

Where those documents enumerate, this one derives: the goal is that after
reading it you could re-invent each stage's algorithm, not just use its API.

## Version and scope

Grammar and library behaviour described here match go1.23 and
`golang.org/x/tools v0.30.0`, the versions Gig pins. All SSA listings are
real output from Gig's own frontend
(`frontend.NewBuilder().Build(...)`, which configures
`ssa.NewProgram(fset, ssa.SanityCheckFunctions|ssa.BareInits)`).

## 0. The pipeline at a glance

```
bytes ──scanner──▶ tokens ──parser──▶ AST ──go/types──▶ typed AST + Info
                                                             │
                                                        ssa builder
                                                             ▼
                                              SSA functions + CFG ──▶ interp
```

Each arrow is a genuine representation change:

| Stage | Consumes | Produces | Loses | Gains |
|---|---|---|---|---|
| scan | bytes | token stream | spacing, comments¹ | vocabulary, positions |
| parse | tokens | AST | token boundaries | structure (nesting, precedence) |
| type check | AST | `types.Info` side tables | nothing (AST untouched) | meaning (objects, types, constants) |
| SSA build | typed AST | instructions in basic blocks | statement shape | explicit control and data flow |

¹ comments survive only if the parser is asked to keep them; Gig does not
need them.

Running example, used throughout:

```go
package main

func choose(cond bool) int {
	x := 1
	if cond {
		x = 2
	} else {
		x = 3
	}
	return x
}

func sum(n int) int {
	s := 0
	for i := 1; i <= n; i++ {
		s += i
	}
	return s
}

func escape(n int) *int {
	v := n + 1
	return &v
}
```

`choose` demonstrates the diamond CFG and φ-placement, `sum` the loop CFG
with a back edge, and `escape` why SSA construction cannot lift every
variable out of memory.

## 1. Scanning: bytes → tokens

The scanner (`go/scanner`) is a deterministic finite-state machine driven by
the class of the current byte. Its job is purely lexical: it never knows
whether `x` is a variable, a type, or undeclared — that is three stages away.

### 1.1 The main loop

Conceptually:

```
skip whitespace (space, tab, CR; newline is special — see §1.4)
switch class of current character:
  letter or _        → scan identifier, then check the keyword table
  digit, or '.'+digit → scan a numeric literal
  '"'                → interpreted string literal
  '`'                → raw string literal
  '\''               → rune literal
  '/'                → comment ("//", "/*") or the ÷ family of operators
  otherwise          → operator/delimiter via maximal munch
```

Each token is emitted as a triple `(pos, tok, lit)`: a compact
`token.Pos` (an offset into a `token.FileSet`, decodable to
file:line:column — see `SSA_PIPELINE.md §2.3`), a `token.Token` enum
value, and the literal text where it matters (identifiers, literals,
and inserted semicolons).

### 1.2 Maximal munch

Operators are scanned greedily: the scanner always takes the longest
sequence of characters that forms a token. Seeing `<`, it must look ahead:

```
<     LSS        (less-than)
<-    ARROW      (channel send/receive)
<=    LEQ
<<    SHL
<<=   SHL_ASSIGN
```

The implementation is a cascade of tiny lookahead helpers (a "switch2 /
switch3 / switch4" pattern in `scanner.go`): after consuming `<`, peek one
character; each match consumes and refines, otherwise the shorter token is
emitted. This is why `a<-b` is a channel send but `a < -b` is a comparison
against a negation — the space breaks the munch. No backtracking is ever
needed because Go's operator set is prefix-closed: every proper prefix of an
operator is itself an operator.

### 1.3 Identifiers, keywords, and literals

An identifier is a letter-or-underscore followed by letters, digits, or
underscores (Unicode letters and digits included). After scanning the run,
the scanner consults `token.Lookup`: 25 spellings (`func`, `if`, `range`,
…) are reserved and come back as keyword tokens; everything else is
`IDENT`. Keywords are therefore *not* special-cased in the scanning
automaton — they are ordinary identifier scans with a table lookup bolted
on. This is the standard trick that keeps the DFA small.

Numeric literals are the most intricate automaton: base prefixes
(`0b`, `0o`, `0x`), digit-separating underscores, floating point with
optional exponent (decimal `e`, hexadecimal `p`), and a trailing `i` for
imaginary literals — all resolved into just three token kinds: `INT`,
`FLOAT`, `IMAG`. The scanner validates shape only; the *value* (arbitrary
precision, via `go/constant`) is not computed until the type checker needs
it.

Strings come in two automata: interpreted (`"…"`, escape sequences decoded
for validity, cannot span lines) and raw (`` `…` ``, no escapes, newlines
allowed, carriage returns dropped).

### 1.4 Automatic semicolon insertion

Go's grammar — like C's — terminates statements with semicolons, but Go
source rarely contains them. The reconciliation happens **in the scanner**,
per the spec rule:

> A semicolon is automatically inserted into the token stream at the end of
> a line if the line's final token is: an identifier; an integer, float,
> imaginary, rune, or string literal; one of `break`, `continue`,
> `fallthrough`, or `return`; one of `++` or `--`; or one of `)`, `]`, `}`.

The scanner tracks one bit — "did the previous token end something that can
end a statement?" — and, on reaching a newline with that bit set, emits a
synthetic `SEMICOLON` token whose literal is `"\n"`. Real semicolons have
literal `";"`, so later stages can tell them apart.

This is observable. Scanning `x := 1\nif cond {\n` yields:

```
snippet.go:1:1  IDENT  "x"
snippet.go:1:3  :=     ""
snippet.go:1:6  INT    "1"
snippet.go:1:7  ;      "\n"     ← inserted: line ended after INT
snippet.go:2:1  if     "if"
snippet.go:2:4  IDENT  "cond"
snippet.go:2:9  {      ""       ← no semicolon: '{' is not in the set…
```

…and no semicolon follows `{`, because `{` is not in the final-token set.
This one rule is why Go's brace style is mandatory: writing

```go
func f()
{           // scanner inserted ';' after ')' on the previous line
```

turns the function declaration into `func f();` followed by an orphaned
block — a parse error. The grammar did not forbid Allman braces; the
semicolon rule made them unrepresentable.

The parser leans on inserted semicolons everywhere: statement lists inside
`{}` are parsed as "statement `;` statement `;` …" exactly as if the file
had been written in the explicit-semicolon dialect.

### 1.5 Error tolerance

The scanner never stops. Invalid input produces an `ILLEGAL` token or a
best-guess token plus an entry in a `scanner.ErrorList`, and scanning
continues at the next character. This is a deliberate design for tooling:
an IDE needs the 900 valid tokens after the one bad byte. Gig's study lab
surfaces this directly — `internal/study/analyze.go` collects the
`ErrorList` into diagnostics while still returning the token stream.

## 2. Parsing: how top-down LL(1) parsing works

The parser's job is to impose structure: the flat token stream becomes a
tree in which nesting encodes grouping and precedence. `go/parser` is a
**hand-written recursive-descent parser with one token of lookahead** —
operationally an LL(1) predictive parser for most of the grammar, with a
handful of documented deviations (§2.6). To understand why it is shaped
the way it is, it pays to understand the theory it implements.

### 2.1 Grammars, derivations, and what "top-down" means

A context-free grammar is a set of productions like

```
Stmt → "if" Expr Block ElseOpt | "return" ExprList | …
```

A *derivation* starts from the start symbol and repeatedly replaces a
nonterminal with one of its productions until only tokens remain. A
**top-down** parser builds this derivation from the root: at every step it
holds a partially-expanded tree and must decide *which production to expand
next*. Expanding always the **leftmost** nonterminal while reading input
**left to right** with **1** token of lookahead gives the name **LL(1)**.

The entire discipline of LL(1) parsing is about making that "which
production?" decision with only the current token — no backtracking, no
peeking further.

### 2.2 FIRST, FOLLOW, and the prediction table

Two sets make the decision mechanical:

- **FIRST(α)** — the set of tokens that can begin a string derived from α
  (plus ε if α can derive the empty string).
- **FOLLOW(A)** — the set of tokens that can appear immediately after
  nonterminal A in any derivation.

From them one builds the **prediction table** `M[A, a]`: expanding
nonterminal A when the lookahead is token a,

- if `a ∈ FIRST(α)` for production `A → α`, predict `A → α`;
- if `A → α` can derive ε and `a ∈ FOLLOW(A)`, predict that ε-production.

A grammar is *LL(1)* precisely when this table has at most one entry per
cell. Two properties break it:

1. **Common prefixes** — `A → xy | xz` forces a two-token decision. Fixed
   by *left factoring*: `A → x A'; A' → y | z`.
2. **Left recursion** — `E → E + T | T` makes the parser expand E forever
   without consuming input. Fixed by rewriting into right recursion with a
   tail nonterminal (§2.3), or — in practice — by iteration (§2.5).

### 2.3 A complete worked example

The classic expression grammar, already left-factored and with left
recursion eliminated:

```
E  → T E'
E' → + T E' | ε
T  → F T'
T' → * F T' | ε
F  → ( E ) | id
```

FIRST/FOLLOW:

| symbol | FIRST | FOLLOW |
|---|---|---|
| E | `(` `id` | `)` `$` |
| E′ | `+` ε | `)` `$` |
| T | `(` `id` | `+` `)` `$` |
| T′ | `*` ε | `+` `)` `$` |
| F | `(` `id` | `*` `+` `)` `$` |

Prediction table (blank = syntax error):

| | `id` | `+` | `*` | `(` | `)` | `$` |
|---|---|---|---|---|---|---|
| **E** | E→TE′ | | | E→TE′ | | |
| **E′** | | E′→+TE′ | | | E′→ε | E′→ε |
| **T** | T→FT′ | | | T→FT′ | | |
| **T′** | | T′→ε | T′→*FT′ | | T′→ε | T′→ε |
| **F** | F→id | | | F→(E) | | |

Parsing `id + id * id` with an explicit stack (top at left; `$` marks
bottom/end):

| stack | remaining input | action |
|---|---|---|
| `E $` | `id + id * id $` | predict E→TE′ |
| `T E′ $` | `id + id * id $` | predict T→FT′ |
| `F T′ E′ $` | `id + id * id $` | predict F→id |
| `id T′ E′ $` | `id + id * id $` | match `id` |
| `T′ E′ $` | `+ id * id $` | `+` ∈ FOLLOW(T′): predict T′→ε |
| `E′ $` | `+ id * id $` | predict E′→+TE′ |
| `+ T E′ $` | `+ id * id $` | match `+` |
| `T E′ $` | `id * id $` | predict T→FT′ |
| `F T′ E′ $` | `id * id $` | predict F→id |
| `id T′ E′ $` | `id * id $` | match `id` |
| `T′ E′ $` | `* id $` | predict T′→*FT′ |
| `* F T′ E′ $` | `* id $` | match `*` |
| `F T′ E′ $` | `id $` | predict F→id |
| `id T′ E′ $` | `id $` | match `id` |
| `T′ E′ $` | `$` | predict T′→ε |
| `E′ $` | `$` | predict E′→ε |
| `$` | `$` | **accept** |

Every decision consulted exactly one lookahead token. Note *where* the
grammar put the multiplication: under the T that is E′'s second child —
precedence fell out of the grammar's layering (E handles `+`, T handles
`*`), not out of any numeric comparison.

### 2.4 Recursive descent: the table compiled into code

A recursive-descent parser is the same machine with the stack replaced by
the host language's call stack and the table rows replaced by `switch`
statements:

```go
func parseF() Expr {
	switch tok {          // ← this switch IS row F of the table
	case ID:   return matchID()
	case LPAREN:
		match(LPAREN); e := parseE(); match(RPAREN); return e
	default:   syntaxError()
	}
}
```

`go/parser` is exactly this shape. The parser object holds the one-token
lookahead as three fields — `p.tok`, `p.lit`, `p.pos` — advanced by
`p.next()`. Its `expect(tok)` is the `match` operation: verify the
lookahead, report an error if wrong, advance regardless (see §2.7).
`parseStmt`'s big `switch p.tok` is the prediction row for the Statement
nonterminal: `if` predicts `parseIfStmt`, `for` predicts `parseForStmt`,
`IDENT`/literals/operators that can begin an expression predict
`parseSimpleStmt`, and so on — a literal transcription of FIRST sets into
`case` clauses.

### 2.5 Expressions: precedence climbing instead of grammar layering

The E/T/F encoding works, but has two costs: a grammar with one layer per
precedence level (Go has five binary levels — it would need five
nonterminals), and right-leaning tails (the `E'` chain) that must be
massaged back into the **left-associative** trees Go requires (`a-b-c`
must mean `(a-b)-c`). Production parsers instead collapse all binary
levels into one loop, *precedence climbing* — an LL parser where the
prediction consults the lookahead's precedence, not just its identity:

```go
func (p *parser) parseBinaryExpr(prec1 int) ast.Expr {
	x := p.parseUnaryExpr()
	for {
		oprec := p.tok.Precedence()  // ||=1 &&=2 ==,<,…=3 +,-,|,^=4 *,/,%,<<,…=5
		if oprec < prec1 {
			return x
		}
		op := p.tok
		p.next()
		y := p.parseBinaryExpr(oprec + 1) // +1 ⇒ left associativity
		x = &ast.BinaryExpr{X: x, Op: op, Y: y}
	}
}
```

Trace of `a + b*c` (start: `parseBinaryExpr(1)`):

```
outer(min=1): x = a
  lookahead '+' has prec 4 ≥ 1 → consume '+', recurse with min 5
    inner(min=5): x = b
      lookahead '*' has prec 5 ≥ 5 → consume '*', recurse with min 6
        innermost(min=6): x = c; next lookahead prec < 6 → return c
      x = (b * c); next lookahead prec < 5 → return (b*c)
  x = (a + (b*c))
  next lookahead prec < 1 → return
```

Result: `+` at the root, `b*c` nested as its right child — the same tree
the E/T/F table produced, in one function. The `oprec+1` in the recursion
is the left-associativity knob: the recursive call refuses to consume
another operator of the *same* level, so `a-b-c` loops in the outer frame
(`(a-b)` first) instead of recursing (`a-(b-c)`). Unary operators simply
bind tighter than every binary level, and `parseUnaryExpr` recurses into
itself for prefix chains like `-*p`.

This is worth internalizing: **iteration replaces the eliminated left
recursion**. The theory says "rewrite `E → E + T` right-recursively"; the
practice says "keep a loop and fold leftward as you go". Both are LL —
the loop makes the same one-lookahead predictions the table would.

### 2.6 Where Go bends strict LL(1)

Go's grammar was co-designed with its parser and is *close* to LL(1), but
a hand-written parser affords targeted exceptions:

- **Composite literal vs. block.** In `if x == T{…} {`, is `T{…}` a
  composite literal or is `{` the if-body? The parser tracks whether it is
  inside a control-clause header (an expression-level counter, `exprLev`)
  and refuses bare composite literals there; you must parenthesize:
  `if x == (T{…})`. The ambiguity is resolved by *context state*, not
  lookahead.
- **Type vs. expression.** In `x := (T)(v)` or a conversion versus a call,
  syntax alone cannot distinguish type names from value names. The parser
  parses the more general form and leaves disambiguation to the type
  checker (which is why `ast.CallExpr` may turn out to be a conversion —
  `types.Info.Types[fun].IsType()` tells you which; see
  `SSA_PIPELINE.md §4.6`).
- **Generics.** `f[T]` in expression position could be indexing or
  instantiation. Since go1.18 the parser produces `IndexExpr`/
  `IndexListExpr` and again lets the type checker decide.
- **Labels.** `name:` starting a statement requires deciding between a
  label and an expression after having consumed `name` — a two-token
  pattern handled by inspecting the token *after* the identifier, a
  bounded LL(2) island.

None of these grow into backtracking; each is one extra bit of state or
one extra token of peek, applied at a known trouble spot.

### 2.7 Error recovery

`expect` reports and *continues*; on a bad prediction the parser calls its
sync routine, which discards tokens until one from a synchronizing set
(statement starters like `if`/`for`/`return`, or declaration starters like
`func`/`var`/`type`) and resumes, planting `ast.BadExpr`/`BadStmt`/
`BadDecl` where the hole was. The result is the same tolerance the scanner
has: one error yields a diagnostic plus a mostly-usable tree, which is why
Gig's study lab can show you the AST of a file that does not compile.

The output of this stage is pure syntax: `x` is an `*ast.Ident` with a
position and a spelling, nothing more. Two `x`s in different scopes are
indistinguishable except by position. Meaning is the next stage's job.

## 3. Type checking: the go/types passes

`go/types` answers, for every identifier and expression, three questions:
*which declaration does this refer to?* (resolution), *what type does it
have?* (inference/checking), and *is this combination legal?*
(verification). It writes the answers into side tables (`types.Info`)
without modifying the AST.

The checker is **not** N whole-program tree walks; it is a phase pipeline
in which the middle phase is demand-driven. `Checker.Files` runs, in
order (full detail per pass: `GO_TYPES_CHECKER_PASSES.md`):

### 3.1 `initFiles` — file admission

Resets per-run state, verifies every file declares the same package name,
and pins each file's effective language version. Nothing is resolved yet.

### 3.2 `collectObjects` — declaration collection

One walk over the top of each file (declarations only, not function
bodies). For every `const`/`var`/`type`/`func` declaration it creates an
**object shell** — a `types.Const`, `types.Var`, `types.TypeName`, or
`types.Func` that has a name and a scope entry but *no type yet* — and
inserts it into the package scope. File scopes receive the imports:
each `import` path is resolved through the configured `types.Importer`.

> **The Gig twist.** In a normal compiler the importer loads other
> packages' export data. In Gig, `host.Environment` *is* the
> `types.Importer`: an import of `"strings"` resolves to a synthetic
> `types.Package` built from registered reflect metadata
> (`importer/`, bridged by `host.FromRegistry`). This is the exact seam
> where "compile-time package identity" is bound to "runtime callable
> host function" — and why unregistered imports fail at type-check time,
> which is Gig's sandbox boundary.

After this pass, name → object works everywhere, but objects are untyped
shells.

### 3.3 `packageObjects` — demand-driven typing with cycle detection

Now each package-level object is typed by `objDecl`. The order is not
textual: typing `var x = f()` *demands* typing `f` first, so the checker
recurses along the dependency graph. To catch illegal cycles it colors
objects **white** (untouched) → **grey** (in progress) → **black** (done);
hitting a grey object again means a cycle, legal only through certain
type constructions (a struct may point to itself; a `const` may not
depend on itself).

Three kinds of work happen inside:

- **Types**: named types are built as `*types.Named` whose underlying
  type is resolved lazily — this is what lets recursive types work.
- **Constants**: evaluated *now*, at arbitrary precision, via
  `go/constant`. `const base = 2 + 3` stores exact value `5`; constant
  expressions never survive to runtime (SSA will inline `5`).
  Go's **untyped constant** rules live here: `2 + 3` has default type
  `int` but adapts (`var f float64 = 2 + 3` is exact, no conversion).
- **Function signatures**: parameter and result types, receivers, type
  parameters.

### 3.4 Function bodies — checked last, with a delay queue

Bodies are deferred until all package-level names are typed, so bodies can
reference anything in any order. Body checking is conventional scoped tree
walking: open a `types.Scope` per block, resolve identifiers innermost-out,
check each expression bottom-up. Each checked expression gets an
**operand mode** (is it a value? a constant? a type? something
addressable?) and a type; each statement is verified against its context
(assignability, comparability, range-ability, and so on).

Some obligations cannot be settled inline (interface method-set
completeness, instantiation validity); the checker appends closures to a
`delayed` action queue and drains it at phase end.

### 3.5 `InitOrder` — the dependency-sorted initializer plan

Finally the checker topologically sorts package-level variable
initializers by dependency (`Info.InitOrder`). This is not bookkeeping —
it is *the* specification of what `init` means, and the SSA builder
compiles the synthetic `init` function directly from it.

### 3.6 What the next stage receives

`types.Info` maps, keyed by AST nodes:

| map | answers |
|---|---|
| `Types[expr]` | type + operand mode + exact constant value if any |
| `Defs[ident]` / `Uses[ident]` | which object an identifier declares / refers to |
| `Selections[sel]` | what `x.f` means: which field/method, through which embedding path |
| `Implicits[node]` | objects with no written identifier (e.g. `t := x.(type)` case vars) |
| `Scopes[node]` | the lexical scope tree |
| `InitOrder` | dependency-ordered initializers |

Identity, not spelling, is the currency from here on: the two `x`s in
`choose` and any other function are different `types.Var` objects, and
everything downstream keys on the object.

## 4. SSA and CFG construction

`x/tools/go/ssa` converts each function's typed AST into **static single
assignment** form: a graph of basic blocks (the CFG) whose instructions
each define at most one value, and where every value has exactly one
definition site. Gig builds it with
`ssautil.BuildPackage` → `ssa.NewProgram(fset, SanityCheckFunctions|BareInits)`:
`SanityCheckFunctions` re-verifies structural invariants after every
function is built; `BareInits` builds plain `init` functions without the
`init$guard` boilerplate or calls into dependency packages — correct for
Gig because a Unit is a single package whose imports live on the host side
and were initialized when the host process started.

### 4.1 Why SSA at all

In the AST, `x` in `choose` is one name mutated in three places; asking
"which assignment does this `return x` see?" requires flow analysis. In
SSA that question is *pre-answered by construction*: every use points
directly at its unique definition. That is what makes an interpreter (or
optimizer) simple — data flow is explicit edges, control flow is explicit
blocks.

### 4.2 The builder: syntax-directed lowering with a block cursor

The builder walks the typed AST once, maintaining a **current block**
cursor. Value-producing expressions are lowered postorder — operands
first, then the instruction that combines them, appended to the current
block. Control statements *end* the current block with an explicit
terminator and switch the cursor:

- `if c { T } else { E }` — emit `If c` terminating the current block
  with two successor edges; build T and E each in fresh blocks; both
  `Jump` to a fresh *done* block; cursor continues there.
- `for init; cond; post { B }` — blocks for loop-head (condition),
  body, and done; the body (after the post-statement) `Jump`s back to the
  head: the **back edge** that makes it a loop.
- `return e` — `Return` terminator; the cursor is now dead (a fresh
  unreachable block absorbs any trailing code, deleted later).
- `&&`/`||` — become CFG diamonds too (short-circuit is control flow),
  which is why the AST's `BinaryExpr` for `&&` does not survive as a
  single SSA instruction.

Evaluation order is compiled *into the instruction order*: Go's spec
promises left-to-right evaluation of operands and function calls, and the
builder freezes that promise into the linear instruction sequence. After
building, trivial blocks are jump-threaded and unreachable blocks deleted
(`optimizeBlocks`), then blocks are renumbered.

The result so far is a **CFG in naive memory form**.

### 4.3 Naive form: every variable is memory

The builder's first cut treats every local like C treats a stack slot:

- declaring `x` emits `Alloc` (an addressable cell; `t = local int (x)`),
- every read is a load (`UnOp{Op: MUL}` — the `*addr` dereference),
- every write is a `Store`.

`choose` in naive form (conceptually):

```
entry: t0 = local int (x)
       Store t0 ← 1
       If cond → then, else
then:  Store t0 ← 2 ; Jump done
else:  Store t0 ← 3 ; Jump done
done:  t1 = Load t0
       Return t1
```

Correct, unambiguous — and not yet SSA: `t0`'s cell is assigned three
times. The interesting algorithm is the next one.

### 4.4 Lifting: dominators, φ-placement, renaming

**Lifting** (in `lift.go`, implementing the classic Cytron et al.
construction) promotes memory cells to SSA registers. Three sub-steps:

**(a) Dominator tree.** Block A *dominates* B if every path from entry to
B passes through A. The package computes the dominator tree with a
semidominator (Lengauer–Tarjan family) algorithm over the CFG. Intuition
for `choose`: entry dominates everything; neither branch dominates
`done` (you can reach `done` around either one).

**(b) φ placement at iterated dominance frontiers.** The *dominance
frontier* DF(A) is the set of blocks where A's dominance stops — the
first blocks reachable from A that A does not dominate. These are exactly
the *join points* where two versions of a variable can meet. For each
`Alloc`, take the set S of blocks that store to it; the places needing a
φ-function are the iterated dominance frontier DF⁺(S) (iterated because a
φ is itself a new definition, possibly forcing φs further on). φs that
would merge identical or dead values are pruned.

In `choose`: stores to `x` happen in `entry`, `then`, `else`;
DF(then) = DF(else) = {done}; so one φ lands at the top of `done`.

**(c) Renaming.** A depth-first walk over the dominator tree rewrites
every load into a direct reference to the *reaching definition* — the
innermost store version on the walk's version stack — and deletes the
now-unreferenced `Alloc`s, `Store`s, and loads. Every remaining value now
has exactly one definition: SSA achieved.

The lifted, real output for `choose` (Gig's actual SSA):

```
func choose(cond bool) int:
0:                                entry P:0 S:2
	if cond goto 1 else 3
1:                              if.then P:1 S:1
	jump 2
2:                              if.done P:2 S:0
	t0 = phi [1: 2:int, 3: 3:int] #x        int
	return t0
3:                              if.else P:1 S:1
	jump 2

block 0: preds=[]    succs=[1 3]
block 1: preds=[0]   succs=[2]
block 2: preds=[1 3] succs=[]
block 3: preds=[0]   succs=[2]
```

Note what lifting did: `x := 1` vanished entirely (that version never
reaches a use), the two live versions became φ operands, and the memory
cell is gone.

**Reading a φ.** `phi [1: 2:int, 3: 3:int]` is *not* a runtime merge of
arbitrary data — it selects by *incoming edge*. Its operands correspond
**positionally** to `block.Preds`: `done.Preds = [1 3]`, so arriving from
block 1 yields `2:int`, from block 3 yields `3:int`. All φs at a block's
head notionally execute *simultaneously* on edge entry (they read
old-iteration values before writing new ones), which is why Gig's
interpreter resolves a block's φs in a stage-then-commit step using the
frame's remembered predecessor
(`internal/interp/frame.go` `prevBlock`, `plan.go` `runBlockPhis`).

### 4.5 The loop: back edges and φs that feed themselves

`sum`'s real SSA shows the canonical loop shape:

```
func sum(n int) int:
0:                                entry P:0 S:1
	jump 1
1:                             for.loop P:2 S:2
	t0 = phi [0: 0:int, 2: t3] #s           int
	t1 = phi [0: 1:int, 2: t4] #i           int
	t2 = t1 <= n                           bool
	if t2 goto 2 else 3
2:                             for.body P:1 S:1
	t3 = t0 + t1
	t4 = t1 + 1:int
	jump 1
3:                             for.done P:1 S:0
	return t0

block 0: preds=[]    succs=[1]
block 1: preds=[0 2] succs=[2 3]     ← 2→1 is the back edge
block 2: preds=[1]   succs=[1]
block 3: preds=[1]   succs=[]
```

The loop header is a join of *entry* and *back edge*, so both loop-carried
variables get φs there: on the first arrival (from block 0) `s`/`i` are
`0`/`1`; on every subsequent arrival (from block 2) they are the previous
iteration's `t3`/`t4`. Two mutable source variables became four immutable
values plus two φs; "increment i" became "define a new value `t4` and
route it around the back edge". This block is also precisely the shape
Gig's plan layer fast-paths: φs resolved at block entry, then
`planIntBinOp`/`planIf`/`planJump` ops that never touch the generic
dispatcher (see `internal/interp/plan.go`).

### 4.6 What lifting refuses: addressability escapes

Lifting is only sound for cells whose every access the builder can see.
`escape` takes the address of `v` and returns it:

```
func escape(n int) *int:
0:                                entry P:0 S:0
	t0 = new int (v)      *int      ← heap Alloc: NOT lifted
	t1 = n + 1:int
	*t0 = t1                        ← explicit Store survives
	return t0
```

`&v` means some unknown consumer may read or write the cell later, so it
must remain real memory: the `Alloc` stays (marked heap), stores and loads
stay explicit. **SSA form in x/tools is therefore a hybrid**: registers
where possible, explicit memory where addressability demands it. Gig's
interpreter mirrors the split exactly — lifted values live in
`frame.values` slots; surviving `Alloc`s hold addressable
`reflect.Value` pointers in those same slots (the model documented at the
top of `internal/interp/composite.go`).

### 4.7 The CFG is a byproduct — and the contract

Notice that nothing "generated the CFG" as a separate act: basic blocks
and edges *are* the lowered form of control statements (§4.2), and
lifting merely decorated the existing graph with φs. What the pipeline
hands to a consumer is, per function:

- `fn.Blocks` — basic blocks in a stable order, `Blocks[0]` = entry;
- per block: straight-line `Instrs` (φs first, one terminator last),
  `Preds`/`Succs` edges;
- `fn.Recover` — the optional out-of-line landing block for functions
  with `defer`/`recover` (entered only during panic unwinding);
- synthetic members alongside source functions: `init` (compiling
  `Info.InitOrder`), method wrappers (`$bound`, `$thunk`) that adapt
  method values and interface dispatch.

Everything the interpreter needs is closed over this structure: execution
is "walk `Instrs`, branch by setting the current block, resolve φs from
the predecessor edge" — which is, in one sentence, what
`internal/interp` does.

## 5. Where to go next

- Execute-side counterpart: `docs/ARCHITECTURE.md` (how `internal/interp`
  walks this SSA) and the header of `internal/interp/plan.go` (why the
  typed fast-path layer exists, with measurements).
- Poke at every stage interactively: `go run ./cmd/ssa-study` serves the
  study lab — the token/AST/types/SSA panes are live views of exactly the
  representations described here (`docs/SSA_STUDY_LAB.md`).
- Exhaustive references: `SSA_PIPELINE.md` (APIs and Gig integration),
  `GO_TYPES_CHECKER_PASSES.md` (checker passes at full depth),
  `AST_SSA_REFERENCE.md` (node and instruction catalogs).
