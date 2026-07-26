# Go AST and x/tools SSA Reference Manual

This manual is the exhaustive companion to
[From Go Source to SSA](./SSA_PIPELINE.md). The pipeline report explains how
the phases cooperate; this document is a field guide to every public concrete
AST node and every SSA instruction in the versions selected below.

## Version and scope

- The AST catalog targets Go 1.26.3 and the public
  [go/ast](https://pkg.go.dev/go/ast) API.
- The SSA catalog targets
  [golang.org/x/tools/go/ssa v0.30.0](https://pkg.go.dev/golang.org/x/tools@v0.30.0/go/ssa),
  the version pinned by [go.mod](../go.mod#L1-L10).
- “SSA” means the source-analysis IR in x/tools, not the compiler backend IR
  in cmd/compile/internal/ssa.
- The AST inventory covers all 57 exported concrete types implementing
  ast.Node. Helper types that are not nodes are identified separately.
- The SSA inventory covers all 37 concrete types implementing
  ssa.Instruction. Value-only nodes and instruction helpers are documented
  separately.

## 1. How to read Go AST nodes

### 1.1 Interfaces and concrete nodes

Every AST node provides two half-open source positions:

~~~go
type Node interface {
    Pos() token.Pos // first character belonging to the node
    End() token.Pos // first character immediately after the node
}
~~~

Four marker interfaces classify most nodes. Type syntax is deliberately part
of Expr because a type can appear where grammar expects an expression-shaped
operand, such as the function in a conversion T(x).

~~~mermaid
flowchart TD
    N["ast.Node<br/>Pos() and End()"] --> E["ast.Expr"]
    N --> S["ast.Stmt"]
    N --> D["ast.Decl"]
    N --> P["ast.Spec"]
    N --> A["Auxiliary nodes"]

    E --> EV["Value expressions<br/>Ident, CallExpr, BinaryExpr, ..."]
    E --> ET["Type expressions<br/>ArrayType, StructType, FuncType, ..."]
    S --> SS["Simple statements"]
    S --> SC["Control and clause statements"]
    D --> DD["BadDecl, GenDecl, FuncDecl"]
    P --> PP["ImportSpec, ValueSpec, TypeSpec"]
    A --> AA["Comments, directives, fields, files, packages"]
~~~

The interfaces are closed to outside packages by unexported marker methods
such as exprNode and stmtNode. You can inspect nodes through the interfaces,
but cannot define a new expression type in another package.

### 1.2 Positions, nil fields, and semantic side tables

- Pos and End describe syntax extents. They do not contain filenames; translate
  them with the same token.FileSet used by the parser.
- Optional grammar parts are represented by nil fields or token.NoPos. For
  example, IfStmt.Else may be nil and a default CaseClause has a nil List.
- Distinct Ident pointers with the same Name are distinct syntax occurrences.
  go/types.Info.Defs and Uses connect them to semantic object identity.
- The parser may emit BadExpr, BadStmt, or BadDecl while recovering from
  syntax errors.
- Deprecated ast.Object and ast.Scope links are normally absent when parsing
  with parser.SkipObjectResolution. Modern clients use go/types.

### 1.3 Semantic maps commonly associated with nodes

| types.Info field | AST key | Semantic information |
|---|---|---|
| Types | Expr | Type, exact constant value, and operand mode |
| Defs | declaring Ident | Object introduced by the declaration |
| Uses | referencing Ident | Object denoted by the occurrence |
| Implicits | Node | Objects without an explicit declaring identifier |
| Selections | SelectorExpr | Field or method selection path and receiver adjustment |
| Scopes | selected Node | Lexical scope opened by the node |
| Instances | Ident | Generic type arguments and instantiated type |

## 2. Complete Go 1.26.3 AST catalog

The fields listed below are the fields that control meaning. Documentation and
comment fields are included when they affect ownership or printing.

### 2.1 Comments, directives, fields, files, and packages

| Node | Important fields | Example or role | Meaning and invariants |
|---|---|---|---|
| [Comment](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L68)<!-- AST:Comment --> | Slash, Text | <code>// note</code> | One line or block comment. Text includes the comment markers. |
| [CommentGroup](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L78)<!-- AST:CommentGroup --> | List | Consecutive comments | A group with no intervening tokens or empty lines; Text normalizes markers for documentation. |
| [Directive](https://github.com/golang/go/blob/go1.26.3/src/go/ast/directive.go#L32)<!-- AST:Directive --> | Tool, Name, Args, Slash, ArgsPos | <code>//go:generate stringer</code> | Parsed explicitly with ast.ParseDirective; it is not automatically substituted for Comment nodes. DirectiveArg is a helper, not an ast.Node. |
| [Field](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L201)<!-- AST:Field --> | Doc, Names, Type, Tag, Comment | <code>X, Y int</code> | Used for struct fields, interface methods, receivers, parameters, results, and type parameters. An empty Names list denotes an embedded or unnamed field. |
| [FieldList](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L234)<!-- AST:FieldList --> | Opening, List, Closing | <code>(x int, y string)</code> | Delimited list of Fields. Opening and Closing may be NoPos for an undelimited result. |
| [File](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L1064)<!-- AST:File --> | Doc, Package, Name, Decls, FileStart/FileEnd, Imports, Comments, GoVersion | One source file | Root of a parsed file. Comments contains every parsed group in lexical order; Imports is a convenience index into Decls. Scope and Unresolved are deprecated. |
| [Package](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L1099)<!-- AST:Package --> | Name, Scope, Imports, Files | A set of files | Deprecated syntax-level package aggregate. Pos and End return NoPos; use go/types.Package for semantic package information. |

### 2.2 Value expressions

| Node | Important fields | Minimal source | Meaning, typing, and common lowering |
|---|---|---|---|
| [BadExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L286)<!-- AST:BadExpr --> | From, To | malformed expression | Parser recovery placeholder spanning From:To. Type checking normally reports an error; valid SSA is not built from it. |
| [Ident](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L291)<!-- AST:Ident --> | NamePos, Name, Obj | <code>x</code> | Identifier occurrence. Defs or Uses determines whether it denotes a variable, function, constant, type, package, label, or builtin. Obj is deprecated. |
| [Ellipsis](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L300)<!-- AST:Ellipsis --> | Ellipsis, Elt | <code>...T</code> | Ellipsis in array length or parameter type. Call-site <code>f(xs...)</code> is instead recorded by CallExpr.Ellipsis. |
| [BasicLit](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L316)<!-- AST:BasicLit --> | ValuePos, Kind, Value | <code>42</code>, <code>"go"</code> | Literal spelling and token kind. go/types computes the exact constant.Value; constants usually become ssa.Const rather than instructions. |
| [FuncLit](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L324)<!-- AST:FuncLit --> | Type, Body | <code>func(x int) int { return x }</code> | Anonymous function. SSA creates a nested Function and emits MakeClosure only when bindings or a first-class closure value are needed. |
| [CompositeLit](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L330)<!-- AST:CompositeLit --> | Type, Lbrace, Elts, Rbrace, Incomplete | <code>Point{X: 1}</code> | Composite construction. Elts are expressions, commonly KeyValueExpr. Lowering depends on type: allocation/stores, field/index addresses, MakeMap/MapUpdate, or slice creation. |
| [ParenExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L339)<!-- AST:ParenExpr --> | Lparen, X, Rparen | <code>(a + b)</code> | Preserves explicit grouping. Usually emits no SSA node by itself; lowering uses X. |
| [SelectorExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L346)<!-- AST:SelectorExpr --> | X, Sel | <code>x.Field</code>, <code>x.Method</code> | Package qualification, field selection, method value, or method expression. Info.Selections disambiguates non-package selectors and records embedded-field indices. |
| [IndexExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L352)<!-- AST:IndexExpr --> | X, Lbrack, Index, Rbrack | <code>x[i]</code>, <code>F[int]</code> | Single index or single generic type argument. Types/Instances decides whether this is value indexing or instantiation. May lower to Index, IndexAddr, or Lookup. |
| [IndexListExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L361)<!-- AST:IndexListExpr --> | X, Lbrack, Indices, Rbrack | <code>Pair[int, string]</code> | Generic instantiation with multiple type arguments. Info.Instances records the instantiated type; it is not a runtime indexing operation. |
| [SliceExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L369)<!-- AST:SliceExpr --> | X, Lbrack, Low, High, Max, Slice3, Rbrack | <code>s[lo:hi:max]</code> | Two- or three-index slicing. Omitted bounds are nil. Lowers to ssa.Slice and can panic at runtime for invalid bounds. |
| [TypeAssertExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L382)<!-- AST:TypeAssertExpr --> | X, Lparen, Type, Rparen | <code>x.(T)</code> | Interface assertion. Type is nil only for the special <code>x.(type)</code> syntax inside a type switch. Usually lowers to TypeAssert or ChangeInterface. |
| [CallExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L390)<!-- AST:CallExpr --> | Fun, Lparen, Args, Ellipsis, Rparen | <code>f(x)</code>, <code>T(x)</code> | Function/method call, builtin invocation, or conversion. Info.Types[Fun].IsType and Uses distinguish them; lowering may emit Call, conversion instructions, allocation, or specialized builtin operations. |
| [StarExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L401)<!-- AST:StarExpr --> | Star, X | <code>*p</code>, <code>*T</code> | Pointer dereference in value context or pointer type in type context. A dereference commonly becomes UnOp(MUL); the type form emits no runtime instruction. |
| [UnaryExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L409)<!-- AST:UnaryExpr --> | OpPos, Op, X | <code>-x</code>, <code>!ok</code>, <code>&x</code>, <code>&lt;-ch</code> | Prefix operation. Arithmetic/logical operations and receive become UnOp; address-of yields an address and may force allocation escape. |
| [BinaryExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L416)<!-- AST:BinaryExpr --> | X, OpPos, Op, Y | <code>a + b</code>, <code>a &amp;&amp; b</code> | Binary operation. Constants fold in go/types; ordinary operations become BinOp. Short-circuit logical operators create CFG branches and a Phi. |
| [KeyValueExpr](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L426)<!-- AST:KeyValueExpr --> | Key, Colon, Value | <code>X: 1</code>, <code>"k": v</code> | Keyed composite-literal element. Its effect is determined by the enclosing literal: field/index store or MapUpdate. |

### 2.3 Type expressions

These six types also implement ast.Expr.

| Node | Important fields | Minimal source | Meaning |
|---|---|---|---|
| [ArrayType](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L447)<!-- AST:ArrayType --> | Lbrack, Len, Elt | <code>[4]int</code>, <code>[]byte</code> | Array when Len is present, slice when Len is nil, inferred array when Len is Ellipsis. go/types supplies the semantic array/slice type. |
| [StructType](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L454)<!-- AST:StructType --> | Struct, Fields, Incomplete | <code>struct{ X int }</code> | Struct syntax. Fields contains named, embedded, and tagged fields; field indices later drive Field and FieldAddr. |
| [FuncType](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L463)<!-- AST:FuncType --> | Func, TypeParams, Params, Results | <code>func[T any](T) error</code> | Function signature syntax. Receivers belong to FuncDecl, not FuncType. go/types produces a types.Signature. |
| [InterfaceType](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L471)<!-- AST:InterfaceType --> | Interface, Methods, Incomplete | <code>interface{ Read([]byte) (int, error) }</code> | Method and type-element list. go/types computes the completed type set and method set. |
| [MapType](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L478)<!-- AST:MapType --> | Map, Key, Value | <code>map[string]int</code> | Map type syntax. Runtime construction and access use MakeMap, Lookup, and MapUpdate. |
| [ChanType](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L485)<!-- AST:ChanType --> | Begin, Arrow, Dir, Value | <code>chan int</code>, <code>&lt;-chan int</code> | Channel type and direction. Begin is the first token; Arrow records the directional arrow. |

### 2.4 Statements

| Node | Important fields | Minimal source | Meaning and common lowering |
|---|---|---|---|
| [BadStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L628)<!-- AST:BadStmt --> | From, To | malformed statement | Parser recovery placeholder; no valid SSA should be built after the syntax error. |
| [DeclStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L633)<!-- AST:DeclStmt --> | Decl | <code>var x = 1</code> | GenDecl used as a statement inside a function. Locals begin as allocations/stores and may later be lifted. |
| [EmptyStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L641)<!-- AST:EmptyStmt --> | Semicolon, Implicit | <code>;</code> | Empty statement, explicit or implied. It has no runtime effect. |
| [LabeledStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L647)<!-- AST:LabeledStmt --> | Label, Colon, Stmt | <code>again: for {}</code> | Labels a statement. Info.Defs associates Label with a types.Label; SSA resolves branches to blocks. |
| [ExprStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L656)<!-- AST:ExprStmt --> | X | <code>f()</code> | Expression evaluated only for effects. Go restricts which expressions are legal statements. |
| [SendStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L661)<!-- AST:SendStmt --> | Chan, Arrow, Value | <code>ch &lt;- v</code> | Channel send; lowers to ssa.Send. |
| [IncDecStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L668)<!-- AST:IncDecStmt --> | X, TokPos, Tok | <code>i++</code> | Statement-only increment/decrement. Lowered as read, BinOp with one, then store/current-value update. |
| [AssignStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L677)<!-- AST:AssignStmt --> | Lhs, TokPos, Tok, Rhs | <code>a, b = b, a</code>, <code>x := 1</code> | Assignment or short declaration. Builder evaluates locations and RHS values before writes to preserve parallel assignment and evaluation order. |
| [GoStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L685)<!-- AST:GoStmt --> | Go, Call | <code>go f()</code> | Starts a call in a new goroutine; lowers to ssa.Go. |
| [DeferStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L691)<!-- AST:DeferStmt --> | Defer, Call | <code>defer f()</code> | Evaluates call operands now and schedules the call; lowers to Defer plus RunDefers/recovery control. |
| [ReturnStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L697)<!-- AST:ReturnStmt --> | Return, Results | <code>return x, nil</code> | Explicit or bare return. Lowering performs conversions, runs defers where required, and emits Return. |
| [BranchStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L705)<!-- AST:BranchStmt --> | TokPos, Tok, Label | <code>break outer</code>, <code>goto done</code> | break, continue, goto, or fallthrough. SSA resolves its target and emits a Jump or switch-specific edge. |
| [BlockStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L712)<!-- AST:BlockStmt --> | Lbrace, List, Rbrace | <code>{ work() }</code> | Ordered statement list and lexical scope boundary. It does not imply an SSA block one-for-one. |
| [IfStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L719)<!-- AST:IfStmt --> | If, Init, Cond, Body, Else | <code>if x := f(); x &gt; 0 {}</code> | Optional init, condition, then body, and else statement. Lowers to condition/then/else/join blocks with If, Jump, and possible Phi nodes. |
| [CaseClause](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L728)<!-- AST:CaseClause --> | Case, List, Colon, Body | <code>case 1, 2:</code>, <code>default:</code> | Expression- or type-switch clause. A nil List means default. It participates in the switch's CFG rather than emitting one dedicated instruction. |
| [SwitchStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L736)<!-- AST:SwitchStmt --> | Switch, Init, Tag, Body | <code>switch x { case 1: }</code> | Expression switch; nil Tag means true. Commonly lowers to tests, If/Jump edges, and clause blocks. |
| [TypeSwitchStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L744)<!-- AST:TypeSwitchStmt --> | Switch, Init, Assign, Body | <code>switch v := x.(type) {}</code> | Type switch. Assign must contain the special assertion. Lowering uses type tests/assertions and per-clause bindings. |
| [CommClause](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L752)<!-- AST:CommClause --> | Case, Comm, Colon, Body | <code>case v := &lt;-ch:</code> | Select clause. Comm is a send, receive expression statement, receive assignment, or nil for default. |
| [SelectStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L760)<!-- AST:SelectStmt --> | Select, Body | <code>select { case v := &lt;-ch: }</code> | Channel selection. Lowers to Select, Extract instructions, and CFG dispatch to clause blocks. |
| [ForStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L766)<!-- AST:ForStmt --> | For, Init, Cond, Post, Body | <code>for i := 0; i &lt; n; i++ {}</code> | Three-part or condition-only loop. Nil Cond means true. Header/back-edge joins often contain Phi nodes. |
| [RangeStmt](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L775)<!-- AST:RangeStmt --> | For, Key, Value, TokPos, Tok, Range, X, Body | <code>for k, v := range m {}</code> | Range loop and binding mode. Lowering is type-specific: index loop, Range/Next, channel receive, integer loop, or range-over-function protocol. |

### 2.5 Specifications and declarations

| Node | Important fields | Minimal source | Meaning and semantic links |
|---|---|---|---|
| [ImportSpec](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L909)<!-- AST:ImportSpec --> | Doc, Name, Path, Comment, EndPos | <code>alias "example/p"</code> | One import. Defs or Implicits records the types.PkgName; the importer supplies the referenced types.Package. |
| [ValueSpec](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L920)<!-- AST:ValueSpec --> | Doc, Names, Type, Values, Comment | <code>const A, B = 1, 2</code> | Const or var specification, selected by its enclosing GenDecl.Tok. Defs maps each name to types.Const or types.Var. |
| [TypeSpec](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L929)<!-- AST:TypeSpec --> | Doc, Name, TypeParams, Assign, Type, Comment | <code>type Set[T comparable] map[T]bool</code> | Defined type when Assign is NoPos; alias when it contains <code>=</code>. Defs maps Name to types.TypeName. |
| [BadDecl](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L980)<!-- AST:BadDecl --> | From, To | malformed declaration | Parser recovery placeholder; type checking prevents SSA construction. |
| [GenDecl](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L995)<!-- AST:GenDecl --> | Doc, TokPos, Tok, Lparen, Specs, Rparen | <code>var (...)</code>, <code>type T int</code> | General import, const, type, or var declaration. Tok determines which concrete Spec forms are valid. |
| [FuncDecl](https://github.com/golang/go/blob/go1.26.3/src/go/ast/ast.go#L1005)<!-- AST:FuncDecl --> | Doc, Recv, Name, Type, Body | <code>func (p *Point) X() int {}</code> | Function or method declaration. Defs maps Name to types.Func; SSA creates a Function skeleton before building Body. Body is nil for declarations without source bodies. |

## 3. How to read x/tools SSA

### 3.1 Containers

| Type | Role |
|---|---|
| Program | Owns packages, method sets, wrappers, and the shared FileSet. |
| Package | Owns top-level Members and the synthetic init function. |
| Function | Owns parameters, free variables, local allocations, basic blocks, anonymous functions, and optional recovery block. |
| BasicBlock | Ordered instruction list plus Preds and Succs CFG edges. |
| Member | Package-level Function, Global, NamedConst, or Type. |

### 3.2 Node, Value, and Instruction

~~~mermaid
flowchart TD
    N["ssa.Node<br/>String, Pos, Parent, Operands"] --> V["ssa.Value<br/>Name, Type, Referrers"]
    N --> I["ssa.Instruction<br/>Block"]
    V --> VO["Value-only nodes<br/>Const, Global, Function,<br/>Builtin, Parameter, FreeVar"]
    V --> VI["Value-producing instructions<br/>register values"]
    I --> VI
    I --> EO["Effect-only instructions"]
~~~

- An Instruction appears in exactly one BasicBlock and Block returns that owner.
- A Value has one defining node. Constants, parameters, globals, and functions
  are values but are not instructions.
- A value-producing instruction implements both interfaces. Its printed tN name
  is diagnostic; pointer identity is semantic.
- Operands returns pointers to operand slots so transformation passes can
  rewrite them.
- Referrers is optional reverse-use information and may be nil.
- A tuple is one SSA Value. Extract reads an individual component.
- Phi.Edges[i] corresponds exactly to Phi.Block().Preds[i].
- A reachable block ends in Jump, If, Return, or Panic.

### 3.3 Value-only nodes and helpers

| Type | Meaning |
|---|---|
| [Const](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L476) | Exact go/constant value plus static type; nil Value represents a typed nil. |
| [Global](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L486) | Address-valued package variable. Reading or writing it requires load/store behavior. |
| [Builtin](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L513) | Reference to a predeclared builtin used as a call target. |
| [Function](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L335) | Function code value; may also be a package Member. |
| [Parameter](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L446) | Formal parameter value defined at function entry. |
| [FreeVar](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L434) | Captured value supplied by an enclosing MakeClosure. |
| [CallCommon](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1455) | Shared call description used by Call, Go, and Defer: callee/value, method, arguments, and invoke mode. |
| [SelectState](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L989) | One send or receive alternative consumed by Select. It is not an instruction. |

Call, Go, and Defer also implement CallInstruction, whose Common method exposes
their CallCommon uniformly.

## 4. Complete x/tools v0.30.0 SSA instruction catalog

Gig status is based on
[visitInstr](../internal/interp/ops.go#L20-L135) and the optimized planner in
[plan.go](../internal/interp/plan.go#L89-L166). “Direct” means the interpreter
has an explicit handler; it does not imply every operand type or host boundary
is accepted.

### 4.1 Value-producing instructions

| Instruction | Operands and result | Semantics and typical source | Gig |
|---|---|---|---|
| [Alloc](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L547)<!-- SSA:Alloc --> | No value operand; result is <code>*T</code>. Heap and Comment describe placement/debug intent. | Allocates zeroed storage for locals, address-taken temporaries, new(T), and composite literals. Non-escaping cells may be removed by lifting. | Direct: runAlloc. |
| [Phi](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L566)<!-- SSA:Phi --> | Edges; result has the merged type. | Selects Edges[i] when control arrives from Preds[i]. All Phi nodes precede non-Phi instructions and are conceptually simultaneous. | Block-entry path runBlockPhis; defensive no-op in visitInstr. |
| [Call](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L587)<!-- SSA:Call --> | CallCommon; result is one value or a tuple for zero/multiple results. | Static call, function-value call, method call, interface invoke, or builtin call. Tuple components require Extract. | Direct: runCall. |
| [BinOp](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L599)<!-- SSA:BinOp --> | X, Y, Op; result is the typed operation result. | Arithmetic, bitwise, shift, comparison, and non-short-circuit binary operations. <code>&amp;&amp;</code>/<code>&#124;&#124;</code> use CFG, not BinOp. | Direct; plain int/bool forms may use planIntBinOp. |
| [UnOp](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L631)<!-- SSA:UnOp --> | X, Op, CommaOk; receive may return a tuple. | Logical/bitwise negation, numeric negation, pointer load, or channel receive. | Direct; IndexAddr plus load may be fused for []int. |
| [ChangeType](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L664)<!-- SSA:ChangeType --> | X; result has target Type. | Value-preserving named/underlying, compatible pointer, channel-direction, or generic substitution change. Cannot fail dynamically. | Direct: runChangeType. |
| [Convert](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L698)<!-- SSA:Convert --> | X; result has target Type. | Representation/value conversion involving basic types, strings/slices, numeric kinds, or unsafe pointer forms. Constants are usually folded earlier. | Direct: runConvert. |
| [MultiConvert](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L720)<!-- SSA:MultiConvert --> | X plus internal from/to type sets; target result. | Generic conversion when source or destination is a type parameter; may select ChangeType, Convert, slice/array conversion behavior at runtime. | Unsupported: reaches visitInstr default error. |
| [ChangeInterface](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L738)<!-- SSA:ChangeInterface --> | Interface X; result is another interface type. | Known-assignable interface-to-interface conversion. Unlike a type assertion it cannot fail. | Direct: runChangeInterface. |
| [SliceToArrayPointer](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L760)<!-- SSA:SliceToArrayPointer --> | Slice X; result is pointer to array. | Explicit <code>(* [N]T)(slice)</code>-style slice-to-array-pointer conversion; panics when the slice is too short. | Unsupported: reaches visitInstr default error. |
| [MakeInterface](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L782)<!-- SSA:MakeInterface --> | Concrete X; result is an interface value. | Boxes a concrete dynamic value and type into an interface. A typed nil produces a non-nil interface. | Direct: runMakeInterface. |
| [MakeClosure](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L799)<!-- SSA:MakeClosure --> | Fn plus Bindings; result is a function value. | Closure or bound method. Bindings correspond to Fn.FreeVars in order. | Direct: runMakeClosure. |
| [MakeMap](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L817)<!-- SSA:MakeMap --> | Optional Reserve; result is a map. | <code>make(map[K]V, n)</code> or map literal allocation. | Direct: runMakeMap. |
| [MakeChan](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L834)<!-- SSA:MakeChan --> | Size; result is a channel. | <code>make(chan T, n)</code>; zero size is unbuffered. | Direct: runMakeChan. |
| [MakeSlice](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L856)<!-- SSA:MakeSlice --> | Len and Cap; result is a slice. | Allocates backing array and slice header for <code>make([]T, len, cap)</code>. | Direct: runMakeSlice. |
| [Slice](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L878)<!-- SSA:Slice --> | X plus optional Low, High, Max; result string or slice. | Two- or three-index slicing of string, slice, or pointer-to-array. Invalid bounds panic. | Direct: runSlice. |
| [FieldAddr](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L903)<!-- SSA:FieldAddr --> | Pointer-to-struct X and numeric Field; result is field pointer. | Addressable field selection or struct-literal initialization. Nil X panics. | Direct: runFieldAddr. |
| [Field](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L922)<!-- SSA:Field --> | Struct X and numeric Field; result is field value. | Non-addressable/read-only struct field selection. | Direct: runField. |
| [IndexAddr](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L945)<!-- SSA:IndexAddr --> | Array pointer or slice X and integer Index; result is element pointer. | Addressable array/slice index. Maps and strings are not addressable. | Direct; load/store pair may use the []int fused plan. |
| [Index](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L961)<!-- SSA:Index --> | Array/string/slice-like X and integer Index; result is element value. | Non-addressable index, including string bytes. | Direct: runIndex. |
| [Lookup](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L980)<!-- SSA:Lookup --> | Map X and key Index; result value or <code>(value, ok)</code> tuple. | Map read, including comma-ok form. Extract reads tuple components. | Direct: runLookup. |
| [Select](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1034)<!-- SSA:Select --> | SelectState list and Blocking flag; result tuple. | Performs one ready send/receive, or returns index -1 for nonblocking default. Tuple contains chosen index, receive status, and receive values. | Direct: runSelect. |
| [Range](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1052)<!-- SSA:Range --> | X; result is opaque iterator. | Creates iterator for map or string. Other range kinds may lower to explicit loops rather than Range. | Direct: runRange. |
| [Next](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1075)<!-- SSA:Next --> | Iter and IsString; result <code>(ok, key, value)</code> tuple. | Advances Range iterator. Components require Extract. | Direct: runNext. |
| [TypeAssert](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1120)<!-- SSA:TypeAssert --> | Interface X, AssertedType, CommaOk; result value or tuple. | Runtime interface assertion. Non-comma-ok failure panics; comma-ok failure yields zero and false. | Direct: runTypeAssert. |
| [Extract](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1136)<!-- SSA:Extract --> | Tuple and numeric Index; result is that component. | Reads results of multi-result calls, Lookup, TypeAssert, Select, Next, or receive. | Direct: runExtract. |

### 4.2 Effect-only instructions

| Instruction | Operands and CFG rules | Semantics and typical source | Gig |
|---|---|---|---|
| [Jump](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1154)<!-- SSA:Jump --> | No value operands; owning block has one successor. Must terminate the block. | Unconditional edge for structured flow, branch statements, and synthesized joins. | Direct; may use planJump. |
| [If](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1170)<!-- SSA:If --> | Boolean Cond; Succs[0] is true, Succs[1] false. Must terminate the block. | Conditional branch for if, loops, switches, and short-circuit expressions. | Direct; plain bool conditions may use planIf. |
| [Return](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1197)<!-- SSA:Return --> | Results length equals signature results; no successors. | Returns zero or more values. A ready-made tuple cannot be returned as multiple results without extracting components. | Direct: runReturn. |
| [RunDefers](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1215)<!-- SSA:RunDefers --> | No operands. | Pops and invokes deferred calls, usually before return or in recovery paths. Multiple occurrences on one path are legal. | Direct: runRunDefers. |
| [Panic](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1233)<!-- SSA:Panic --> | Interface value X; no successors and terminates block. | Initiates panic. <code>go panic(x)</code> and <code>defer panic(x)</code> are calls instead. | Direct: runPanic. |
| [Go](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1251)<!-- SSA:Go --> | CallCommon. | Starts a goroutine that performs the described call. It produces no call results. | Direct: runGo. |
| [Defer](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1275)<!-- SSA:Defer --> | CallCommon and optional DeferStack. | Pushes an evaluated call onto a defer stack. | Direct: runDefer. |
| [Send](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1289)<!-- SSA:Send --> | Chan and X. | Sends X on Chan and may block. | Direct: runSend. |
| [Store](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1306)<!-- SSA:Store --> | Addr and Val. | Writes arbitrary typed value through an address. Lifting removes stores to promotable local cells. | Direct; IndexAddr store may use fused []int plan. |
| [MapUpdate](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1322)<!-- SSA:MapUpdate --> | Map, Key, Value. | Implements <code>m[k] = v</code> and map-literal entries. | Direct: runMapUpdate. |
| [DebugRef](https://github.com/golang/tools/blob/v0.30.0/go/ssa/ssa.go#L1363)<!-- SSA:DebugRef --> | Expr, X, IsAddr, optional source object. | Pseudo-instruction mapping source expressions to SSA values/addresses. Emitted only in debug mode and has no dynamic effect. | Explicitly ignored by planner/visitInstr. |

## 5. AST-to-SSA lowering guide

There is intentionally no one-to-one conversion table. AST gives syntax;
go/types.Info supplies meaning; the SSA builder may emit no instruction, one
instruction, multiple instructions, or an entire CFG.

~~~mermaid
flowchart LR
    AST["AST node"] --> DECIDE["SSA builder decision"]
    INFO["types.Info<br/>type, constant, object,<br/>selection, instance"] --> DECIDE
    DECIDE --> ZERO["No runtime instruction<br/>type syntax, constants,<br/>parentheses"]
    DECIDE --> ONE["One primary instruction<br/>BinOp, Call, Lookup, ..."]
    DECIDE --> MANY["Several instructions<br/>Call + Extract,<br/>address + Store"]
    DECIDE --> CFG["Basic blocks and edges<br/>If, Jump, Phi"]
~~~

| Source AST | Type-checking fact | Representative SSA |
|---|---|---|
| BasicLit or constant BinaryExpr | Types[expr].Value is non-nil | Const value; no runtime arithmetic instruction |
| Ident | Uses/Defs object kind | Const, Function, Global, Parameter/FreeVar, builtin, or local current value/load |
| UnaryExpr | Operator and operand type | UnOp, address value, or folded Const |
| BinaryExpr | Constant status and operator | BinOp; <code>&amp;&amp;</code>/<code>&#124;&#124;</code> instead create If/Jump/Phi |
| CallExpr | Fun is type, builtin, static function, method, or interface method | Conversion family, specialized builtin lowering, Call, or invoke-mode Call |
| SelectorExpr | Selections kind and index path | Field/FieldAddr, method function, MakeClosure, or invoke call |
| IndexExpr | Collection type versus generic instantiation | Index, IndexAddr, Lookup, or no runtime operation for instantiation |
| SliceExpr | X type and bounds | Slice |
| TypeAssertExpr | Asserted type and comma-ok context | TypeAssert or ChangeInterface, followed by Extract when tuple-valued |
| CompositeLit | Literal type and addressability | Alloc/Store, FieldAddr/IndexAddr, MakeSlice/Slice, or MakeMap/MapUpdate |
| FuncLit | Captured object set | Nested Function, and MakeClosure when a closure value is required |
| AssignStmt/IncDecStmt | LHS addressability and object identity | RHS values plus Store/MapUpdate, or renamed register values after lifting |
| IfStmt | Typed boolean Cond | If, then/else blocks, Jump, and possible Phi at join |
| ForStmt | Loop shape | Header/body/post/exit blocks, If/Jump, loop-carried Phi |
| RangeStmt | Type of X | Explicit index loop, Range/Next, receive loop, integer loop, or range-function protocol |
| SwitchStmt/TypeSwitchStmt | Tag and case semantics | Tests/assertions plus If/Jump and clause blocks; there is no switch instruction |
| SelectStmt | Clause communications and default | Select, Extract, and CFG dispatch |
| GoStmt/DeferStmt | CallCommon resolved from CallExpr | Go or Defer |
| ReturnStmt | Function signature and result conversions | RunDefers when needed, then Return |

## 6. Gig integration and instruction support

Gig composes the phases in
[defaultBuilder.Build](../internal/frontend/builder.go#L45-L130):

1. create one token.FileSet and parse an ast.File;
2. populate go/types.Info and check the package;
3. create the SSA Program with SanityCheckFunctions and BareInits;
4. create imported type-only packages and the source SSA package;
5. call Package.Build.

[DebugDump](../debug_dump.go#L19-L58) exposes the built functions for teaching
and diagnosis without executing init. The interpreter compiles block-local
plans in [plan.go](../internal/interp/plan.go#L89-L166) and sends generic
operations through [visitInstr](../internal/interp/ops.go#L20-L135).

### 6.1 Current support summary

| Classification | Instructions |
|---|---|
| Direct handlers | Alloc, Call, ChangeInterface, ChangeType, Convert, Defer, Extract, Field, FieldAddr, Go, Index, IndexAddr, Lookup, MakeChan, MakeClosure, MakeInterface, MakeMap, MakeSlice, MapUpdate, Next, Panic, Range, Return, RunDefers, Select, Send, Slice, Store, TypeAssert, UnOp |
| Direct plus optimized plan | BinOp, If, Jump; IndexAddr paired with UnOp or Store for plain []int |
| Special scheduling/metadata | Phi is selected at block entry; DebugRef is ignored |
| Unsupported by current visitInstr | MultiConvert, SliceToArrayPointer |

Unsupported means execution reaches the default error:

~~~text
interp: <function>: unsupported instruction <type> at <instruction>
~~~

It does not mean the parser or SSA builder rejects the corresponding Go source.
For example, a legal slice-to-array-pointer conversion can build successfully
and fail only if Gig executes the unsupported instruction.

## 7. Completeness inventories

The invisible AST and SSA markers in each catalog row make completeness
machine-checkable without confusing repeated explanatory mentions.

### 7.1 Authoritative AST inventory

The 57 documented AST markers are:

~~~text
ArrayType AssignStmt BadDecl BadExpr BadStmt BasicLit BinaryExpr BlockStmt
BranchStmt CallExpr CaseClause ChanType CommClause Comment CommentGroup
CompositeLit DeclStmt DeferStmt Directive Ellipsis EmptyStmt ExprStmt Field
FieldList File ForStmt FuncDecl FuncLit FuncType GenDecl GoStmt Ident IfStmt
ImportSpec IncDecStmt IndexExpr IndexListExpr InterfaceType KeyValueExpr
LabeledStmt MapType Package ParenExpr RangeStmt ReturnStmt SelectStmt
SelectorExpr SendStmt SliceExpr StarExpr StructType SwitchStmt TypeAssertExpr
TypeSpec TypeSwitchStmt UnaryExpr ValueSpec
~~~

Extract the Go 1.26.3 public Node implementations with:

~~~bash
go doc -all go/ast |
sed -nE 's/^func \([^)]*\*([A-Za-z0-9_]+)\) (Pos|End)\(\).*/\1 \2/p' |
sort -u |
awk '{ seen[$1]++ } END { for (name in seen) if (seen[name] == 2) print name }' |
sort
~~~

### 7.2 Authoritative SSA instruction inventory

The 37 documented SSA markers are:

~~~text
Alloc BinOp Call ChangeInterface ChangeType Convert DebugRef Defer Extract
Field FieldAddr Go If Index IndexAddr Jump Lookup MakeChan MakeClosure
MakeInterface MakeMap MakeSlice MapUpdate MultiConvert Next Panic Phi Range
Return RunDefers Select Send Slice SliceToArrayPointer Store TypeAssert UnOp
~~~

Extract the x/tools v0.30.0 instruction implementations with:

~~~bash
go doc -all golang.org/x/tools/go/ssa |
sed -nE 's/^func \([^)]*\*([A-Za-z0-9_]+)\) Block\(\).*/\1/p' |
sort -u
~~~

Types such as CallCommon and SelectState are omitted from this second inventory
because they do not implement Instruction. Const, Global, Builtin, Function,
Parameter, and FreeVar are omitted because they are Values but not
Instructions.
