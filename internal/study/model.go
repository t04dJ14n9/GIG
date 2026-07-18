// Package study exposes structured teaching views of Go's source-to-SSA
// pipeline. It analyzes source but never executes it.
package study

// Result contains every pipeline stage that completed for one source file.
type Result struct {
	Source      string       `json:"source"`
	Tokens      []Token      `json:"tokens"`
	AST         []ASTNode    `json:"ast"`
	Types       []TypeFact   `json:"types"`
	Functions   []Function   `json:"functions"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// SourceRange is a half-open byte range plus one-based source coordinates.
type SourceRange struct {
	Start     int `json:"start"`
	End       int `json:"end"`
	Line      int `json:"line"`
	Column    int `json:"column"`
	EndLine   int `json:"endLine"`
	EndColumn int `json:"endColumn"`
}

// Token is one scanner token. Inserted semicolons have an empty Text range.
type Token struct {
	Kind  string      `json:"kind"`
	Text  string      `json:"text"`
	Range SourceRange `json:"range"`
}

// ASTNode is one entry in a preorder, flattened syntax tree.
type ASTNode struct {
	ID      int         `json:"id"`
	Parent  int         `json:"parent"`
	Depth   int         `json:"depth"`
	Kind    string      `json:"kind"`
	Excerpt string      `json:"excerpt"`
	Range   SourceRange `json:"range"`
}

// Diagnostic reports a phase-specific source problem.
type Diagnostic struct {
	Phase   string `json:"phase"`
	Message string `json:"message"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
}

// TypeFact associates semantic information with an AST occurrence.
type TypeFact struct {
	ASTID  int         `json:"astId"`
	Kind   string      `json:"kind"`
	Name   string      `json:"name,omitempty"`
	Type   string      `json:"type,omitempty"`
	Value  string      `json:"value,omitempty"`
	Object string      `json:"object,omitempty"`
	Range  SourceRange `json:"range"`
}

// Function is a source function and its SSA control-flow graph.
type Function struct {
	Name      string       `json:"name"`
	Signature string       `json:"signature"`
	Raw       string       `json:"raw"`
	Blocks    []BasicBlock `json:"blocks"`
}

// BasicBlock is one SSA block and its indexed CFG edges.
type BasicBlock struct {
	Index        int           `json:"index"`
	Comment      string        `json:"comment,omitempty"`
	Preds        []int         `json:"preds"`
	Succs        []int         `json:"succs"`
	Instructions []Instruction `json:"instructions"`
}

// Instruction is a presentation-safe SSA instruction record.
type Instruction struct {
	Kind     string      `json:"kind"`
	Text     string      `json:"text"`
	Result   string      `json:"result,omitempty"`
	Type     string      `json:"type,omitempty"`
	Operands []string    `json:"operands"`
	ASTID    int         `json:"astId"`
	Range    SourceRange `json:"range"`
}
