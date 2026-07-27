# Go 1.26.3 `go/types`: The Checker Passes, One by One

This manual follows every stage executed by Go 1.26.3's `go/types` package
after parsing has produced a complete package AST. It explains what each stage
receives, which algorithm it uses, which checker fields and `types.Info` maps
it changes, which errors it can report, and what invariant it establishes for
the next stage.

The central source function is
[`Checker.checkFiles`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L499).
Keep it open while reading this document.

## How many passes are there?

`go/types` does not formally advertise an N-pass compiler architecture. In Go
1.26.3, however, `checkFiles` invokes ten named stages and then one conditional
generic-instantiation analysis:

```go
check.initFiles(files)
check.collectObjects()
check.sortObjects()
check.directCycles()
check.packageObjects()
check.processDelayed(0)
check.cleanup()
check.initOrder()
check.unusedImports() // unless disabled
check.recordUntyped()
check.monomorph()     // only if no earlier type error
```

This manual calls those the **11 checker passes**. They are not 11 complete
AST traversals. Some walk declarations or function bodies, while others sort a
list, finish lazy type state, or analyze a graph built by earlier stages.

| # | Stage | Kind | Principal result |
|---:|---|---|---|
| 1 | `initFiles` | package setup | Accepted files, package name, effective versions |
| 2 | `collectObjects` | declaration collection | File scopes, imports, object shells, `declInfo` |
| 3 | `sortObjects` | deterministic ordering | Source-ordered `objList` |
| 4 | `directCycles` | graph validation | Direct type-name cycles rejected |
| 5 | `packageObjects` | declaration type checking | Package object types, constants, signatures, dependencies |
| 6 | `processDelayed` | body and delayed checking | Function bodies, local scopes, remaining validations |
| 7 | `cleanup` | type finalization | Publishable aliases, named types, interfaces, type parameters |
| 8 | `initOrder` | dependency scheduling | `Info.InitOrder`, initialization-cycle errors |
| 9 | `unusedImports` | usage validation | Per-file unused-import errors |
| 10 | `recordUntyped` | result finalization | Remaining untyped expressions in `Info.Types` |
| 11 | `monomorph` | generic graph validation | Unbounded instantiation cycles rejected |

After these stages, the checker records the package Go version, marks the
package complete, and releases temporary maps.

## Version and scope

This document follows the local Go 1.26.3 sources under:

```text
/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types
```

It discusses the source-analysis type checker in `go/types`, not the compiler
backend. Much of this implementation is generated from
`cmd/compile/internal/types2`, but the `go/types` copy operates on `go/ast` and
publishes `types.Info`, so it is the relevant implementation for AST-based
tools and the x/tools SSA builder.

## Running example

The passes will be illustrated with this two-file package.

`model.go`:

```go
package study

import "fmt"

type Integer interface {
    ~int | ~int64
}

type Box[T Integer] struct {
    Value T
}

func (b Box[T]) String() string {
    return fmt.Sprint(b.Value)
}

var Multiplier = Base + 1
var Base = 2

func MakeBox[T Integer](x T) Box[T] {
    return Box[T]{Value: x * T(Multiplier)}
}
```

`use.go`:

```go
package study

import "fmt"

func Use(input any, flag bool) (int, bool) {
    n := 1
    if flag {
        n, ok := input.(int)
        return n + Multiplier, ok
    }

    box := MakeBox(n)
    fmt.Println(box.String())

    table := map[string]int{"answer": box.Value}
    value, ok := table["answer"]
    return value, ok
}
```

This package exercises:

- two distinct file-scope imports of the same package;
- a constraint interface and a generic named type;
- a generic receiver and method;
- an initializer referring to a later declaration;
- a syntactic call that is semantically a type conversion;
- generic inference and method selection;
- shadowing through a short declaration;
- a comma-ok map lookup; and
- initialization ordering that differs from source order.

## Checker state to recognize

The main mutable structure is
[`Checker`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L156).
It contains three broad categories of state.

### Long-lived package state

```text
conf      checker configuration
fset      source position table
pkg       semantic package and package scope
Info      optional public result maps
objMap    Object -> declaration metadata
objList   source-ordered objects
impMap    importer cache
```

### State for the current `Files` call

```text
files          accepted AST files
versions       effective version per file
imports        every local PkgName import object
methods        receiver TypeName -> pending methods
untyped        expressions whose final type is not known yet
delayed        queued semantic actions
cleaners       types requiring final cleanup
usedVars       local/package variables observed as used
usedPkgNames   import objects observed as used
mono           generic type-flow graph
```

### Current checking environment

[`environment`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L74)
changes while declarations and bodies are checked:

```text
decl       current package declaration, for dependency recording
scope      innermost scope used by lexical lookup
version    accepted Go version for current file
iota       current iota value, if checking a constant declaration
sig        current function signature, if inside a function
```

### The object lifecycle

Package objects normally move through these states:

```text
Pass 2: shell exists, Object.Type() == nil
Pass 5: declaration is pending on objPath
Pass 5: Object.Type() and constant value/signature become known
Pass 7: lazy internal type state is made safe to publish
```

That separation—object identity first, semantic type second—is fundamental to
forward references and recursive declarations.

---

# Pass 1: `initFiles`

Source:

- [`checkFiles` invocation](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L521)
- [`initFiles`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L337)
- [`Config.GoVersion`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/api.go#L127)

## Purpose

Establish which AST files belong to this package-checking run and which Go
language version applies to each file.

## Input

The caller has already supplied:

```go
checker.Files([]*ast.File{modelFile, useFile})
```

`NewPackage` has already created an empty package scope whose parent is the
Universe scope. No file scopes or package objects exist yet.

## Algorithm

### 1. Reset per-run state

`Checker.Files` may be called incrementally, so `initFiles` clears data that
must not leak from a prior call:

```text
files, imports, dotImportMap
firstErr, methods, untyped, delayed
objPath, objPathIdx, cleaners
usedVars, usedPkgNames
```

The object map and package itself are longer-lived because incremental calls
may extend a package.

### 2. Determine and validate the package name

The first accepted non-blank package name establishes `pkg.name` if it was
empty. Every later file must use the same name. A file with a mismatching name
receives a diagnostic and is omitted from `check.files`.

For the running example:

```text
model.go package name = study -> accepted
use.go   package name = study -> accepted
pkg.name = study
```

The package-clause identifier `_` is rejected.

### 3. Select the package language version

`Config.GoVersion` supplies the package default. A version newer than the
checker understands produces an error. An empty version disables ordinary
language-version checks.

### 4. Select each file's effective version

Every file starts with `Config.GoVersion`. If `ast.File.GoVersion` contains a
valid version derived from file build constraints, that file-specific value is
used, with the compatibility floor described in the source as `go1.21`.

The result is stored in:

```go
check.versions[file] = effectiveVersion
```

If `Info.FileVersions` was allocated, the checker reuses and populates that
same map.

## `types.Info` effects

Only `Info.FileVersions` may be populated. There are no definitions, uses,
scopes, or expression types yet.

## Errors first detectable here

- invalid package name `_`;
- files with mismatching package names;
- configured or file-specific versions newer than the checker.

## Output invariant

Every element of `check.files` belongs to the same package, and every accepted
file has an effective Go version in `check.versions`.

Nothing has been imported, declared, resolved, or type-checked.

---

# Pass 2: `collectObjects`

Source:

- [`collectObjects`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/resolver.go#L216)
- [`declInfo`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/resolver.go#L20)
- [`walkDecls`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/decl.go#L358)
- [`declarePkgObj`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/resolver.go#L103)
- [`Checker.declare`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/decl.go#L16)

## Purpose

Create all file and package bindings before determining declaration types. This
is the declaration-indexing pass that makes forward references possible.

## Input

- accepted files and versions from Pass 1;
- the empty or incrementally populated package scope;
- an importer supplied through `types.Config`;
- package AST declarations whose bodies and expressions are still unchecked.

## Algorithm

### 1. Create one file scope per AST file

For each file, the checker:

1. installs its effective Go version;
2. records the package-clause identifier as `Info.Defs[id] = nil` because a
   package clause introduces no lexical object;
3. creates a `Scope` whose parent is the package scope;
4. uses the complete `token.File` extent when available; and
5. records `Info.Scopes[file] = fileScope`.

The initial scope tree is:

```text
Universe
  package study
    model.go file scope
    use.go file scope
```

Sibling file scopes are never searched from one another.

### 2. Normalize declarations

`walkDecls` turns heterogeneous `GenDecl` and `FuncDecl` syntax into internal
`importDecl`, `constDecl`, `varDecl`, `typeDecl`, and `funcDecl` values. It also
performs declaration arity checks and expands inherited constant metadata.

For:

```go
const (
    A int = iota
    B
)
```

the `B` declaration is represented with the inherited type and initializer
plus its own `iota` index, though the expression is not evaluated yet.

### 3. Import packages into file scopes

An import is validated and loaded through
[`importPackage`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/resolver.go#L136).
The importer returns a semantic `*types.Package` with a package scope.

Each import declaration creates its own local `*types.PkgName`:

```text
model.go fmt PkgName ─┐
                      ├─> one imported *types.Package for fmt
use.go fmt PkgName ───┘
```

An explicitly renamed import is recorded in `Info.Defs`. An ordinary import
has no identifier node for its inferred local name, so it is recorded in
`Info.Implicits[ImportSpec]`.

Normal imports enter only the current file scope. Blank imports create no
binding. Dot imports merge exported imported objects into the file scope and
record which import introduced them so later use can mark that import used.

`pkg.imports` contains unique imported packages for the public package API;
`check.imports` contains every local `PkgName` object for per-file unused
checking.

### 4. Create constant shells

For each package constant, Pass 2 creates a `*types.Const` with a nil final
type and a `declInfo` containing:

```text
containing file scope
effective file version
explicit or inherited type syntax
initializer syntax
inherited-initializer flag
```

Exact constant evaluation is deferred to Pass 5.

### 5. Create variable shells

Each package variable becomes a `*types.Var` of kind `PackageVar` with a nil
type. Its `declInfo` remembers the optional type and initializer AST.

For an N-to-one declaration:

```go
var x, y = pair()
```

both variables share one `declInfo` containing `lhs = [x, y]`. This ensures
that dependencies and `Info.InitOrder` later treat `pair()` as one initializer.

### 6. Create type-name shells

A type declaration creates a `*types.TypeName` with a nil type and stores the
whole `*ast.TypeSpec` in `declInfo`. No `Named`, `Alias`, type parameters,
underlying type, fields, or methods are fully built yet.

### 7. Create function and method shells

A function declaration creates a `*types.Func` whose signature is nil and
stores the `*ast.FuncDecl` in `declInfo`.

Ordinary functions enter the package scope. `init` functions are intentionally
not inserted because they cannot be named and multiple `init` declarations are
legal. Methods are not package-scope bindings either; their names are reached
through receiver method sets.

Methods are retained in `objMap` and temporarily represented by:

```text
method object
pointer-receiver flag
syntactic receiver base identifier
```

### 8. Declare package objects and record definitions

`declarePkgObj`:

1. rejects illegal non-function declarations named `init` or `main` where
   appropriate;
2. inserts the object into the package scope;
3. records `Info.Defs[identifier] = object`;
4. stores `objMap[object] = declInfo`; and
5. assigns a monotonically increasing source-order number.

Duplicate insertion leaves the original binding in place and reports a
duplicate-declaration error.

### 9. Check file/package collisions

Because imports and package declarations occupy different scope maps, the
checker explicitly rejects a package declaration colliding with an import in
a particular file:

```go
import "fmt"
var fmt = 1
```

This comparison occurs after all files are collected, so a collision with a
package declaration in another file is also found.

### 10. Associate methods with receiver type names

Only after all declarations exist does the checker resolve each method's
receiver base name in the package scope. Valid associations are stored in:

```go
check.methods[receiverTypeName] = append(..., method)
```

This permits a method to appear before its receiver type or in another file.
Signature checking and final receiver validation still happen later.

## Running-example state after Pass 2

```text
Universe
  package study
    Integer    -> TypeName, type nil
    Box        -> TypeName, type nil
    Multiplier -> Var, type nil
    Base       -> Var, type nil
    MakeBox    -> Func, signature nil
    Use        -> Func, signature nil

    model.go file scope
      fmt -> first PkgName

    use.go file scope
      fmt -> second PkgName
```

Separately:

```text
pending methods:
    Box -> [String]

objMap:
    each package object and method -> its AST declaration metadata
```

`Multiplier`'s initializer has not been visited, so `Base` has not yet been
recorded as a use or dependency.

## `types.Info` effects

- `Defs`: package declarations, methods, explicit import names, and nil entries
  for package-clause identifiers;
- `Implicits`: ordinary inferred import names;
- `Scopes`: file scopes;
- generally no initializer/body `Uses`, expression `Types`, `Selections`, or
  generic `Instances` yet.

## Errors first detectable here

- invalid or failed imports;
- duplicate declarations and import collisions;
- invalid declaration arity;
- forbidden declaration names;
- preliminary `main`, `init`, receiver, and type-parameter-version errors.

## Output invariant

Every valid package declaration has a stable object identity and is findable
by name, even though most object types are still nil.

---

# Pass 3: `sortObjects`

Source:

- [`sortObjects`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/resolver.go#L502)
- [`Object.order`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/object.go#L42)

## Purpose

Convert the unordered `objMap` into a reproducible source-ordered list for
later declaration processing and deterministic diagnostics.

## Why this pass exists

`objMap` is a Go map:

```go
map[Object]*declInfo
```

Map iteration order is deliberately unspecified. If later phases ranged over
it directly, declaration checking order, selected cycle diagnostics, and tie
breaking could vary between executions.

Pass 2 assigned each object an increasing `order` value when it was collected.
Pass 3 recovers that order.

## Algorithm

The algorithm is only three steps:

```go
check.objList = make([]Object, len(check.objMap))

for obj := range check.objMap {
    append obj to objList
}

sort objList by obj.order()
```

Package-scope constants, variables, types, functions, invisible `init`
functions, and methods tracked by `objMap` all participate.

## What this is not

This is not a dependency sort. For:

```go
var Multiplier = Base + 1
var Base = 2
```

the resulting `objList` still places `Multiplier` before `Base` if that is
their source order. Pass 5 resolves dependencies on demand, and Pass 8 computes
runtime initialization order.

Those are three distinct orderings:

```text
source order             Pass 3
declaration demand order Pass 5
initialization order     Pass 8
```

## Running-example result

An approximate list is:

```text
Integer
Box
String method
Multiplier
Base
MakeBox
Use
```

Exact ordering across declarations follows the order in which Pass 2 walked
the accepted files and declarations.

## `types.Info` effects

None. No AST is traversed, no type is computed, and no definition or use is
recorded.

## Errors

None under a valid internal checker state.

## Output invariant

`objList` contains exactly the keys of `objMap`, sorted by their previously
assigned source-order number.

---

# Pass 4: `directCycles`

Source:

- [`directCycles`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/cycles.go#L12)
- [`directCycle`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/cycles.go#L23)
- [`cycleError`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/decl.go#L287)

## Purpose

Reject direct cycles made entirely of package type names before complete type
construction begins.

## What “direct” means

The pass follows a declaration only when its right side is literally an
`*ast.Ident` naming another package-scope `*types.TypeName`.

It catches:

```go
type A B
type B C
type C A
```

It stops when it encounters syntax such as:

```go
type A *A
type B []B
type C struct{ Next *C }
type D pkg.External
type E Generic[int]
```

Stopping does not declare those examples valid. It means their legality needs
the fuller recursive-type and finite-size algorithms used during Pass 5.

## Algorithm: three-color graph marking

The graph nodes are package `TypeName` objects. `pathIdx` encodes three states:

```text
absent     white: never visited
value >= 0 grey:  on current path; value is path index
value < 0  black: fully processed
```

For every type name in `objList`:

1. if black, stop immediately;
2. if grey, the current path has returned to a prior node, so a cycle exists;
3. otherwise mark the node grey and append it to the path;
4. inspect the declaration RHS;
5. continue only if the RHS is a simple identifier resolving to another
   package-scope type name;
6. after traversal, mark every visited node black.

When a cycle is found, the checker:

- sets the repeated type name's type to `Typ[Invalid]`;
- extracts the cycle segment from the current path;
- rotates diagnostic presentation toward the earliest source declaration; and
- reports each “A refers to B” edge.

## Example trace

```go
type A B
type B C
type C A
```

```text
visit A: white -> grey, path [A]
RHS B:   white -> grey, path [A B]
RHS C:   white -> grey, path [A B C]
RHS A:   already grey at index 0
cycle:   A -> B -> C -> A
mark involved type invalid and report
finish:  A, B, C become black in pathIdx
```

## Why this is separate from `objDecl` cycle detection

Direct alias/name chains are easiest and clearest to diagnose from syntax
before partially built `Named` or `Alias` values complicate the object state.
Pass 5 still uses `objPath` for cycles exposed while recursively checking full
declarations, and `finiteSize` rejects infinitely sized recursive types.

## `types.Info` effects

None directly. This pass works from `TypeSpec` syntax already retained in
`declInfo` and package-scope objects.

## Errors first detectable here

Direct recursive type-name and alias cycles such as:

```go
type A = B
type B = A
```

or equivalent definition chains without an intervening type literal.

## Output invariant

No unreported cycle consisting solely of direct package type-name RHS edges
remains. Invalid cycle participants have an invalid type marker that later
passes can propagate without repeatedly reporting the same root error.

---

# Pass 5: `packageObjects`

Source:

- [`packageObjects`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/resolver.go#L635)
- [`objDecl`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/decl.go#L48)
- [`constDecl`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/decl.go#L408)
- [`varDecl`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/decl.go#L454)
- [`typeDecl`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/decl.go#L517)
- [`funcDecl`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/decl.go#L813)

## Purpose

Determine the semantic types and constant values of all package declarations,
construct function and method signatures, record declaration dependencies, and
queue function bodies for later checking.

This is the largest package-declaration pass, but it deliberately does not
check ordinary function bodies.

## Processing order

`packageObjects` divides source-ordered objects into three groups:

1. non-alias type declarations;
2. alias declarations; and
3. all other objects: constants, variables, functions, and methods.

This ordering reduces cases where an alias is needed before the type it refers
to has a usable representation. Within each group, objects retain source order.

Incremental `Checker.Files` calls receive additional handling: methods may be
attached to named types that were already checked by an earlier call.

## The demand-driven object algorithm

Every object is passed to `objDecl`, but that does not mean dependencies are
checked in list order. `objDecl` uses a three-state object lifecycle:

```text
white: not on objPath and Object.Type() == nil
grey:  currently being checked and present in objPathIdx
black: not on objPath and Object.Type() != nil
```

For a white object, `objDecl`:

1. pushes it on `objPath` and marks it grey;
2. saves the previous checking environment;
3. installs the declaration's file scope and Go version;
4. dispatches according to concrete object kind;
5. recursively checks referenced objects as needed; and
6. pops the object when its type is established.

For a black object, it returns immediately. Encountering a grey object exposes
a potential declaration cycle, which `validCycle` classifies according to the
objects and type definitions involved.

This is why forward references work:

```go
var Multiplier = Base + 1
var Base = 2
```

When `Multiplier`'s initializer resolves `Base`, `ident` sees that `Base.Type()`
is nil and recursively calls `objDecl(Base)` before finishing `Multiplier`.

## Installing the declaration environment

For each package declaration:

```text
check.scope   = declaration's file scope
check.version = declaration file's Go version
check.decl    = current const/var/function declaration when dependencies matter
```

Starting from the file scope ensures lookup sees:

```text
current file imports
  -> package declarations
      -> Universe predeclared names
```

## Checking identifiers and recording dependencies

[`ident`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/typexpr.go#L17)
searches outward through the current scope, records `Info.Uses`, ensures the
referenced object's declaration is checked when necessary, and chooses an
operand mode based on object kind.

When a package initializer or function body references another package object,
[`addDeclDep`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L211)
records:

```text
current declInfo -> referenced package object
```

These edges are consumed by Pass 8.

## Constant declarations

`constDecl`:

1. installs the declaration's `iota` value;
2. gives the object an unknown constant value initially for error resilience;
3. checks an explicit type if present and requires a valid constant type;
4. recursively checks the initializer expression;
5. requires a constant operand;
6. performs implicit or explicit conversion and representability checks; and
7. stores the final type and exact `constant.Value` on the `*types.Const`.

Inherited constant initializers use adjusted error positions because the
physical expression belongs to an earlier `ValueSpec`.

## Variable declarations

`varDecl`:

1. checks an explicit variable type if present;
2. checks the initializer expression with that type as a possible inference
   target;
3. verifies assignment compatibility;
4. defaults untyped values when required;
5. infers the variable type if no explicit type was written; and
6. handles N-to-one initializers through `initVars`.

For the running example, checking `Multiplier` proceeds approximately as:

```text
check BinaryExpr Base + 1
  check Ident Base
    resolve package Var Base
    Base.Type is nil -> recursively objDecl(Base)
      check literal 2
      default/infer Base as int
      finish Base
    record use and dependency Multiplier -> Base
    operand = variable, int
  check literal 1
    operand = constant, untyped int, exact 1
  match 1 to int and check +
  result = non-addressable value, int
infer Multiplier as int
```

## Type declarations

`typeDecl` distinguishes aliases from definitions.

For a definition:

```go
type Box[T Integer] struct { Value T }
```

it:

1. creates and assigns a `*types.Named` early to guard against recursion;
2. opens a type-parameter scope when needed;
3. declares all type parameters before checking their constraints;
4. checks constraints and records their definitions/uses;
5. dispatches the RHS through
   [`typInternal`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/typexpr.go#L230);
6. stores the RHS as the named type's source representation;
7. queues full recursive-type validity checks; and
8. attaches methods collected in Pass 2.

An alias creates a `*types.Alias` and resolves its RHS without introducing a
new defined type identity.

The type-expression dispatcher handles identifiers, qualified names,
instantiations, arrays, slices, structs, pointers, signatures, interfaces,
maps, and channels. It records type expressions in `Info.Types` with
`IsType() == true`.

## Method collection

[`collectMethods`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/decl.go#L726)
adds Pass 2's pending method objects to the completed receiver `Named` type. It
rejects duplicate method names and queues a field/method name-collision check
until the underlying struct is fully available.

The method's own signature is still handled through `funcDecl` like an ordinary
function declaration.

## Function declarations

`funcDecl`:

1. creates an empty `*types.Signature` and assigns it to the function object
   immediately to guard against cycles;
2. calls
   [`funcType`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/signature.go#L155);
3. opens and records the function scope;
4. declares receiver type parameters, function type parameters, receiver,
   parameters, and named results;
5. checks their types and constraints;
6. sets the function scope extent to the entire declaration; and
7. queues the function body with `later` unless bodies are ignored or absent.

After this pass, callers can use the function signature even though its body
has not been checked.

## Expression checking inside initializers

Package constant and variable initializers use the same expression machinery
later used in bodies:

```text
rawExpr
  -> exprInternal concrete AST dispatch
  -> identifier/selector/call/index/binary/etc. algorithm
  -> generic and pending-type validation
  -> record operand in types.Info
```

The internal [`operand`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/operand.go#L54)
contains:

```text
mode: invalid, type, constant, variable, map index, value, comma-ok, ...
expr: originating AST expression
typ:  semantic type
val:  exact constant value if constant
id:   builtin identity if builtin
```

This is how syntax such as `T(x)` becomes a conversion only after `T` resolves
to a type object.

## `types.Info` effects

This pass can populate:

- `Defs` for type parameters, parameters, receiver variables, results, fields,
  and other declarations encountered while building types and signatures;
- `Uses` for identifiers in package initializer and declaration type syntax;
- `Types` for checked type and initializer expressions;
- `Instances` for generic types or functions instantiated in declarations;
- `Selections` for field or method selections in package initializers;
- `Scopes` for type-parameter and function scopes;
- `Implicits` for unnamed parameters and similar declaration-created objects.

Function-body entries mostly wait for Pass 6.

## Errors first detectable here

- undefined names in declaration types and package initializers;
- invalid constant expressions and overflows;
- invalid variable initialization and assignment;
- invalid recursive and infinitely sized types not covered by Pass 4;
- invalid type parameter constraints and instantiations;
- invalid map, struct, interface, signature, and channel type syntax;
- duplicate methods and invalid receiver types;
- invalid function signatures;
- generic or ordinary call errors occurring in package initializers.

## Output invariant

Every package object has a non-nil semantic type, possibly `Typ[Invalid]` after
an error. Constants have values, named types and aliases have representations,
functions and methods have signatures, package initializer dependencies have
been recorded, and function bodies are queued rather than checked.

---

# Pass 6: `processDelayed`

Source:

- [`later`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L260)
- [`processDelayed`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L576)
- [`funcBody`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/stmt.go#L18)
- [`stmt`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/stmt.go#L410)

## Purpose

Execute work that had to wait until package declarations or surrounding syntax
were sufficiently complete. The largest component is function-body checking,
but the queue also contains type validations that require completed types.

## The delayed action structure

Each action stores:

```text
version  effective Go version when queued
f        closure performing the semantic work
desc     optional debug description
```

`later` appends an action to `check.delayed`. Capturing the file version is
essential because Pass 6 may execute actions after the checker has moved across
several files.

## Queue algorithm

`processDelayed(top)` processes actions beginning at index `top`:

```text
save current version
for i := top; i < len(delayed); i++
    restore action's captured version
    execute action
    action may append more actions
truncate delayed back to top
restore previous version
```

The loop condition observes the growing length, so nested function literals
queued by function bodies are also processed.

The package-level invocation uses `top == 0`, meaning all remaining actions.

## Why function bodies are delayed

Other declarations need function signatures early but rarely need bodies.
Delaying bodies guarantees that package object types and signatures are
available before local semantic analysis begins.

For each body, `funcBody` installs:

```text
decl    function's declInfo, for dependency recording
scope   function scope built from its signature
version captured from the declaration file
sig     current function signature
iota    nil for ordinary declared functions
```

It then:

1. walks the outer body statement list;
2. performs specialized label analysis if labels were seen;
3. verifies that a result-bearing function cannot fall off the end; and
4. recursively reports unused local variables.

## Statement tree-walking

`stmt` dispatches on concrete AST node type. Important paths include:

```text
DeclStmt      -> local declaration checking
AssignStmt    -> assignVars or shortVarDecl
ReturnStmt    -> assign results to signature result variables
ExprStmt      -> require call/receive or allowed builtin statement
BlockStmt     -> open lexical block scope
IfStmt        -> open if scope, check init/condition/body
SwitchStmt    -> check tag, cases, comparability, clause scopes
TypeSwitch    -> type-specific implicit variables and clause scopes
For/Range     -> loop scopes, iteration types, assignment
Go/Defer      -> require valid call
BranchStmt    -> use statement-context legality bits
```

The statement context is a bitset describing whether `break`, `continue`, and
`fallthrough` are allowed and whether a case is final or belongs to a type
switch.

## Scope construction during body checking

The function scope was created in Pass 5 and is associated with the
`*ast.FuncType`. It contains type parameters, receiver, parameters, named
results, and declarations in the outermost function body.

`funcBody` walks `body.List` directly, so the outer body `BlockStmt` does not
create a redundant child scope. Nested blocks and control statements do:

```text
file scope
  function scope
    if scope
      if body block scope
    for scope
      for body block scope
```

For the running example, `n, ok := input.(int)` is inside the `if` body's block
scope. `shortVarDecl` uses `scope.Lookup`, not an outward lookup, to decide
same-block redeclaration. Therefore the inner `n` is a new object shadowing the
function-scope `n`.

## Expression checking in bodies

The same `rawExpr` machinery used for package initializers now checks every
body expression. This is when the running example records:

- `MakeBox` and `n` uses;
- inferred instance `MakeBox[int]`;
- method selection `box.String`;
- package-qualified use `fmt.Println`;
- field selection `box.Value`;
- map-index operand mode; and
- comma-ok tuple type for `table["answer"]`.

`shortVarDecl` creates new variable objects and records their `Defs` before RHS
checking, but inserts them into the scope only after the RHS. Thus:

```go
x := x + 1
```

resolves the RHS `x` to an outer declaration, not the new variable.

## Function-body dependency recording

Because `funcBody` installs the function's `declInfo` as `check.decl`, uses of
package constants, variables, and functions inside the body add dependency
edges for Pass 8.

Example:

```go
var X = f()
func f() int { return Y }
var Y = 1
```

Passes 5 and 6 record:

```text
X -> f
f -> Y
```

Pass 8 later removes the function node while preserving the effective `X -> Y`
dependency.

## Non-body delayed checks

Examples of semantic work queued because types may be incomplete include:

- recursive named-type validity;
- map-key comparability;
- whether an interface used as a value type is only a constraint interface;
- field and method name collisions;
- interface and type-set validations;
- some generic constraint and instantiation validations.

For example, a map key type may be a named type whose underlying structure is
not complete at the moment the `Map` object is created. The checker can build
the map type first and verify `Comparable(key)` later.

## Segmented delayed processing

Not every action waits until the package-level call. `stmt` records the current
queue length and defers `processDelayed(top)` so function literals created in a
statement are checked before leaving that statement's scope.

`shortVarDecl` similarly processes RHS function literals before inserting new
variables. This preserves the language's scope-start rules inside closures.

## `types.Info` effects

This is the principal producer of local semantic information:

- `Defs` and `Uses` for local identifiers;
- `Types` for body expressions;
- `Selections` for fields and methods;
- `Instances` for generic calls and types;
- `Scopes` for blocks and control statements;
- `Implicits` for type-switch variables and unnamed objects.

It also updates usage maps and package dependency edges.

## Errors first detectable here

- undefined local names;
- local declaration and assignment errors;
- invalid operators, calls, conversions, selectors, assertions, indexing, and
  composite literals in bodies;
- illegal branch placement and `goto` behavior;
- missing returns;
- unused local variables;
- delayed recursive-type, comparability, method, interface, and constraint
  errors.

## Output invariant

All declared function and method bodies requested by the configuration have
been checked, nested delayed actions have run at the correct scope boundaries,
and all delayed semantic validations queued so far are complete.

---

# Pass 7: `cleanup`

Source:

- [`cleaner` and `needsCleanup`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L290)
- [`Checker.cleanup`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L606)
- [`Alias.cleanup`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/alias.go#L165)
- [`Interface.cleanup`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/interface.go#L153)
- [`Named.cleanup`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/named.go#L364)
- [`TypeParam.cleanup`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/typeparam.go#L108)

## Purpose

Force checker-created types into a safe, publishable state and detach temporary
checker references before clients use the package concurrently.

This is not an AST pass. It operates on semantic type objects registered while
earlier passes built aliases, interfaces, named types, instances, and type
parameters.

## Registration

Checker-aware constructors call:

```go
check.needsCleanup(typeObject)
```

The cleanup list can contain:

```text
*types.Alias
*types.Interface
*types.Named
*types.TypeParam
```

## Algorithm

The checker uses an index loop rather than `range`:

```go
for i := 0; i < len(check.cleaners); i++ {
    check.cleaners[i].cleanup()
}
```

This is intentional: cleaning one named type may expand it and register
additional types that also require cleanup.

Afterward the list is cleared.

## Type-specific cleanup

### Aliases

`Alias.cleanup` calls `unalias` so the cached actual type is fully resolved.
After publication, unaliasing should be a pure read rather than a lazy write
that could race.

### Interfaces

`Interface.cleanup` computes the interface type set, clears the checker pointer
and temporary embedded-position data, and leaves the interface safe for
concurrent use.

### Named types

For an origin named type, `Named.cleanup` forces its underlying type to exist.
Instances may retain lazy underlying expansion. It then clears the checker
pointer.

### Type parameters

`TypeParam.cleanup` converts or exposes the bound as its constraint interface
and clears the checker pointer.

## `types.Info` effects

No new AST mappings are normally created. Existing `Info.Types`, object types,
instances, and selections may point to the semantic objects being finalized.

## Errors

Cleanup primarily establishes internal invariants. User-facing validity errors
should already have been reported by declaration or delayed checking.

## Output invariant

Semantic types escaping through the package, objects, and `types.Info` no
longer depend on mutable checker state for essential completion and are safe for
ordinary concurrent client use.

---

# Pass 8: `initOrder`

Source:

- [`initOrder`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/initorder.go#L19)
- [`dependencyGraph`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/initorder.go#L226)
- [`nodeQueue.Less`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/initorder.go#L323)
- [`addDeclDep`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/check.go#L211)

## Purpose

Compute the legal evaluation order of package variable initializers and report
constant/variable initialization cycles.

This pass does not decide the order in which declarations are type-checked.
Types and bodies are already checked. It computes the runtime initialization
schedule exposed as `Info.InitOrder`.

## Input dependency edges

Passes 5 and 6 added edges to each declaration's `declInfo.deps` whenever an
identifier referenced a package constant, variable, or function.

An edge:

```text
A -> B
```

means “the initializer or body associated with A depends on B.”

Only constants, variables, and functions implement the internal `dependency`
interface. Type-only references do not determine variable initialization
order.

## Build the graph

`dependencyGraph` creates nodes for all package constants, variables, and
functions in `objMap`, including isolated nodes. It adds both successor and
predecessor links for every recorded dependency.

Why include functions? A variable initializer can call a function whose body
reads another variable:

```go
var X = f()
func f() int { return Y }
var Y = 1
```

The raw graph is:

```text
X -> f -> Y
```

## Eliminate function nodes

Recursive functions are legal and are not themselves initialized values. To
avoid treating function recursion as an initialization cycle, the graph removes
function nodes.

For every function, it connects each predecessor directly to each successor:

```text
before: X -> f -> Y
after:  X ------> Y
```

Function nodes are processed by estimated rewiring cost so high-cost removals
happen later. The final graph contains only constants and variables.

## Priority-queue scheduling

Every remaining node receives:

```text
ndeps = number of outstanding dependencies
```

The heap priority is:

1. constants before non-constants;
2. fewer outstanding dependencies first;
3. source order as the tie breaker.

The checker repeatedly pops the highest-priority node, reduces the dependency
count of its dependents, and repairs their heap positions. This is a
priority-aware form of topological scheduling.

In a valid program, the popped node has zero outstanding dependencies after
earlier nodes are removed.

## Cycle detection

If the next node still has `ndeps > 0`, no available node can break the
remaining dependency chain. The checker searches for a path back to the same
object, reports an initialization cycle, then continues to reduce cascading
diagnostics.

Example:

```go
var A = B + 1
var B = A + 1
```

produces:

```text
A -> B
B -> A
```

Neither node reaches zero dependencies, so a cycle is reported.

## Producing `Info.InitOrder`

Only variables with actual initializer expressions are appended. Constants
participate in dependency analysis and cycle reporting but are not runtime
variable initializers.

For:

```go
var x, y = pair()
```

both variable nodes share one `declInfo`. An `emitted` set ensures the shared
initializer appears only once:

```text
x, y = pair()
```

Variables without initializer expressions do not appear.

## Running-example result

Source order is:

```text
Multiplier = Base + 1
Base = 2
```

Dependency order is:

```text
Base = 2
Multiplier = Base + 1
```

Therefore `Info.InitOrder` contains `Base` before `Multiplier`.

## `types.Info` effects

This pass resets and fills `Info.InitOrder`. It does not add expression types,
uses, definitions, or scopes.

## Errors first detectable here

- constant dependency cycles deferred from declaration checking;
- package variable initialization cycles, including dependencies routed
  through function bodies.

## Output invariant

`Info.InitOrder` contains each package variable initializer exactly once in a
dependency-correct order, preserving source order where dependencies do not
force another order.

---

# Pass 9: `unusedImports`

Source:

- [`unusedImports`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/resolver.go#L701)
- [`errorUnusedPkg`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/resolver.go#L718)

## Purpose

Report each non-blank import binding that was never used in the file where it
was declared.

This stage is skipped when:

- `Config.DisableUnusedImportCheck` is true; or
- `Config.IgnoreFuncBodies` is true, because body uses would be missing and the
  result would be unsound.

## Why it must run late

An import may be used only inside a function body:

```go
import "fmt"

func f() {
    fmt.Println("used")
}
```

Pass 2 created the `PkgName`, but Pass 6 did not observe the use until checking
the body. Running unused-import checking earlier would produce a false error.

## Usage tracking

`check.imports` contains every local `PkgName` object, not merely unique
imported packages. This makes the check file-specific.

Normal qualified use marks the package object used when `selector` handles:

```go
fmt.Println
```

Dot-import use is marked when identifier resolution discovers that an exported
object entered the file scope through a particular dot import.

Blank imports are exempt by definition. Fake packages created after an import
failure are marked used to avoid a misleading secondary unused-import error.

## Algorithm

For every collected import object:

```go
if obj.name != "_" && !check.usedPkgNames[obj] {
    check.errorUnusedPkg(obj)
}
```

The diagnostic preserves useful information about renamed imports and unusual
package names.

## Running example

The two `fmt` bindings are independently used:

```text
model.go fmt -> used by fmt.Sprint
use.go   fmt -> used by fmt.Println
```

Removing only `fmt.Println` makes the `use.go` import unused even though
`model.go` still uses its separate `fmt` import object.

## `types.Info` effects

None. The pass reads the internal import and usage sets accumulated earlier.

## Errors first detectable here

Unused normal, renamed, or dot imports after all requested bodies have been
checked.

## Output invariant

Every non-blank local import object is either marked used or has an
unused-import diagnostic.

---

# Pass 10: `recordUntyped`

Source:

- [`record`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/recording.go#L18)
- [`recordUntyped`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/recording.go#L45)
- [`updateExprType`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/expr.go#L251)
- [`recordTypeAndValue`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/recording.go#L59)

## Purpose

Publish `Info.Types` entries for expressions that legitimately remain untyped
after all contextual conversions and defaulting opportunities are known.

## Why recording is deferred

When an expression is first checked, its final public type may still depend on
context.

Consider the literal `1`:

```go
const C = 1        // remains untyped int
var A int64 = 1    // converted to int64 by target
var B = 1          // defaulted to int
```

Recording `1` as untyped immediately in all three cases would make the latter
two `Info.Types` entries wrong.

Therefore [`record`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/recording.go#L18)
does this:

```text
typed operand   -> publish immediately
untyped operand -> remember in check.untyped
```

The remembered `exprInfo` contains:

```text
operand mode
untyped basic type
exact constant value, if any
whether it is the LHS of a delayed shift check
```

## Contextual finalization before Pass 10

Assignments, comparisons, calls, binary operations, and other contexts call
`updateExprType` when they determine an untyped expression's final type.

`updateExprType` may recursively update child expressions. For a nonconstant
binary expression, for example, the result and operands may all acquire the
same target type. It also checks representability and delayed integer-shift
requirements.

Once final, the expression is removed from `check.untyped` and published.

## Final flush algorithm

Pass 10 iterates the entries still in `check.untyped` and calls:

```go
recordTypeAndValue(expr, mode, untypedType, exactValue)
```

Invalid operands are omitted from `Info.Types`. If the caller did not allocate
the `Types` map, there is nothing to publish.

## `types.Info` effects

This pass completes `Info.Types` for valid expressions that retain types such
as:

```text
untyped bool
untyped int
untyped rune
untyped float
untyped complex
untyped string
untyped nil
```

It preserves exact `constant.Value` values.

## Errors

Most untyped conversion and representability errors were reported when a
context finalized the expression. The final flush is primarily result
publication, not a new AST validation pass.

## Output invariant

Every valid checked expression requested by the caller has its correct final
typed or legitimately untyped `TypeAndValue` representation. No expression is
left waiting for a context that can no longer appear.

---

# Pass 11: `monomorph`

Source:

- [`monomorph`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/mono.go#L86)
- [`monoGraph`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/mono.go#L55)
- [`recordInstance`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/mono.go#L174)
- [`reportInstanceLoop`](/opt/homebrew/Cellar/go/1.26.3/libexec/src/go/types/mono.go#L121)

## Purpose

Reject recursive generic instantiation patterns that would require an
unbounded sequence of distinct concrete instantiations in a compiler using
static monomorphization.

This stage runs only when `check.firstErr == nil`. The implementation avoids
running the analysis on a graph that may be incomplete because earlier type
errors interrupted generic checking.

## The problem

Ordinary generic recursion can reach a finite fixed point:

```go
func F[T any]() {
    F[T]()
}
```

Only `F[T]` is required.

Derived recursive instantiation can grow forever:

```go
func F[T any]() {
    F[*T]()
}
```

Static instantiation would demand:

```text
F[int]
F[*int]
F[**int]
F[***int]
...
```

## Graph construction during earlier passes

The graph is accumulated when generic types and functions are instantiated.
Vertices represent:

- local type parameters; and
- some named types declared inside generic functions or methods.

Edges describe type flow into type parameters:

```text
weight 0: the argument is exactly the referenced type
weight 1: the argument contains or derives from the referenced type
```

For example:

```text
T used as T    -> zero-weight flow
T used as *T   -> positive-weight flow
T used as []T  -> positive-weight flow
```

Instantiations of imported type parameters can be ignored because an
instantiation cycle must be contained within one package under Go's generic
model.

## Valid and invalid cycles

A cycle containing only zero-weight edges is allowed: following it does not
construct increasingly complex types.

A cycle containing at least one positive edge is invalid: each traversal can
increase type structure and produce another distinct instantiation.

## Detection algorithm

`monomorph` uses a variant of Bellman-Ford, but searches for greatest-weight
paths rather than shortest paths.

For each edge:

```text
candidateWeight = source.weight + edge.weight
```

If this improves the destination's known weight, the checker records the
predecessor edge and path length. Reaching a path length equal to the number of
vertices proves that an improving path contains a positive-weight cycle.

The algorithm stops early when an entire iteration makes no improvement, which
is the expected case for ordinary code.

## Diagnostic reconstruction

When a loop is found, `reportInstanceLoop` walks predecessor edges backward
until it locates a repeated vertex, trims any noncycle prefix, and reports how
each type parameter was instantiated or how a local named type became
implicitly parameterized.

## Running example

`MakeBox(n)` records an instantiation with `T = int`. There is no edge returning
from `int` to `T`, so the graph has no recursive cycle and this pass succeeds.

## `types.Info` effects

None. `Info.Instances` was populated when instantiations were checked in
Passes 5 and 6. Pass 11 validates the internal graph derived from those events.

## Errors first detectable here

Positive-weight recursive generic-instantiation cycles that are individually
well-typed but cannot reach a finite monomorphization fixed point.

## Output invariant

The package contains no unbounded recursive instantiation cycle detectable by
the generic type-flow graph.

---

# Final package publication

After the 11 stages, `checkFiles`:

```go
check.pkg.goVersion = check.conf.GoVersion
check.pkg.complete = true
```

It then releases temporary memory such as import-tracking, alias, type-set,
usage, and context maps. The public results remain reachable through:

```text
*types.Package
package and nested *types.Scope values
types.Object and types.Type graphs
types.Info side tables
reported diagnostics
```

The checker intentionally continues through many localized errors so these
results may still be partially useful. `Info` documentation warns that maps
may be incomplete for ill-typed code.

# One declaration across all passes

The running example's `Multiplier` declaration provides a compact end-to-end
review:

```go
var Multiplier = Base + 1
var Base = 2
```

| Pass | What happens to `Multiplier` |
|---:|---|
| 1 | `model.go` is accepted and assigned an effective Go version |
| 2 | A package `Var` shell is created with type nil; its `declInfo` remembers `Base + 1` |
| 3 | The object enters deterministic `objList` source order before `Base` |
| 4 | It is ignored because direct type cycles concern only `TypeName` objects |
| 5 | Its initializer is checked; `Base` is checked on demand; both become `int`; edge `Multiplier -> Base` is recorded |
| 6 | No direct body work is needed for this initializer, though function dependencies are completed here |
| 7 | Any named/interface/type-parameter objects reachable from its type are finalized |
| 8 | `Base` is scheduled before `Multiplier` in `Info.InitOrder` |
| 9 | Unrelated except that `model.go`'s `fmt` import must have been used in its method body |
| 10 | Any remaining untyped expression records are flushed; the initializer's contextual types are final |
| 11 | Unrelated unless the declaration participates in recursive generic instantiation |

# Which pass should you inspect for a bug?

| Symptom | Start here |
|---|---|
| wrong file version or mismatched package handling | Pass 1, `initFiles` |
| missing declaration, import, or method association | Pass 2, `collectObjects` |
| nondeterministic declaration diagnostics | Pass 3, `sortObjects` |
| direct alias/type-name recursion accepted | Pass 4, `directCycles` |
| wrong object type, constant value, signature, or package initializer type | Pass 5, `packageObjects` |
| wrong local scope, body expression, branch, selector, or local use | Pass 6, `processDelayed` |
| incomplete interface, alias, named type, or type parameter after checking | Pass 7, `cleanup` |
| wrong variable initialization order or missed init cycle | Pass 8, `initOrder` |
| incorrect unused-import diagnostic | Pass 9, `unusedImports` |
| missing or incorrectly typed untyped expression in `Info.Types` | Pass 10, `recordUntyped` |
| recursive generic type growth accepted | Pass 11, `monomorph` |

# Final mastery checklist

You understand the checker pipeline when you can explain these without
consulting the source:

1. Why does Pass 2 create objects whose types are nil?
2. Why is Pass 3 source ordering different from dependency ordering?
3. Which recursive type cycles can Pass 4 see syntactically, and which must
   wait for Pass 5?
4. How does `objDecl` use white, grey, and black object states?
5. Why are signatures checked in Pass 5 but bodies delayed to Pass 6?
6. How can a function body affect package variable initialization order?
7. Why does cleanup compute interface type sets before publication?
8. How does Pass 8 eliminate recursive function nodes without losing variable
   dependencies?
9. Why must unused imports be checked after bodies?
10. Why can an untyped expression not always be recorded immediately?
11. What distinguishes an allowed zero-weight generic cycle from an invalid
    positive-weight cycle?

The most important overall model is:

```text
collect identities
  -> order and reject simple cycles
  -> determine package declaration semantics on demand
  -> check bodies and validations after global types exist
  -> make type objects safe to publish
  -> schedule initialization and finish diagnostics/results
```

That is how `go/types` turns a syntax-only package AST into the semantic graph
consumed by analysis tools and SSA construction.
