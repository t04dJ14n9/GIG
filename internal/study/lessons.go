package study

// Quiz is one prediction checkpoint in a lesson.
type Quiz struct {
	Prompt    string   `json:"prompt"`
	Options   []string `json:"options"`
	Answer    int      `json:"answer"`
	Correct   string   `json:"correct"`
	Incorrect string   `json:"incorrect"`
}

// Lesson is a guided source-to-SSA experiment.
type Lesson struct {
	ID          string `json:"id"`
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Kicker      string `json:"kicker"`
	Explanation string `json:"explanation"`
	Stage       string `json:"stage"`
	Source      string `json:"source"`
	Quiz        Quiz   `json:"quiz"`
}

// lessonCatalog returns the ordered lesson definitions. It is a
// function rather than a package global so the catalog stays immutable
// from the caller's point of view; Lessons() deep-copies the result.
func lessonCatalog() []Lesson {
	return []Lesson{
		{
			ID:          "positions",
			Number:      1,
			Title:       "Positions are coordinates",
			Kicker:      "go/token",
			Explanation: "A token.Pos is a compact coordinate in a FileSet, not a line number. Select tokens to translate their byte offsets into line and column positions. Inserted semicolons have positions too, even though they add no source bytes.",
			Stage:       "tokens",
			Source: `package main

func twice(n int) int {
	return n * 2
}
`,
			Quiz: Quiz{
				Prompt:    "Where does the func token begin?",
				Options:   []string{"Line 1, column 1", "Line 3, column 1", "Line 3, column 6"},
				Answer:    1,
				Correct:   "Exactly. The package clause and blank line occupy the first two lines; FileSet translation places func at 3:1.",
				Incorrect: "Trace from the first byte and remember that the blank line still advances the line table.",
			},
		},
		{
			ID:          "precedence",
			Number:      2,
			Title:       "Parsing builds precedence",
			Kicker:      "go/parser",
			Explanation: "The parser nests syntax according to operator precedence. Select the BinaryExpr rows and compare their source ranges: multiplication becomes the right child of addition, so the tree preserves the evaluation grouping without extra parentheses.",
			Stage:       "ast",
			Source: `package main

func calc(a, b, c int) int {
	return a + b*c
}
`,
			Quiz: Quiz{
				Prompt:    "Which operator is at the root of the expression tree for a + b*c?",
				Options:   []string{"+", "*", "Both operators share one node"},
				Answer:    0,
				Correct:   "Right. The root is +, with b*c nested as its right operand.",
				Incorrect: "The tighter-binding operator is grouped deeper in the tree; inspect the two BinaryExpr ranges.",
			},
		},
		{
			ID:          "types",
			Number:      3,
			Title:       "Types add meaning",
			Kicker:      "go/types",
			Explanation: "The AST records spellings; types.Info records what they mean. Compare expression facts, exact constants, and Def/Use object strings. The constant expression is evaluated during type checking, while arithmetic involving x remains a runtime operation.",
			Stage:       "types",
			Source: `package main

const base = 2 + 3

func scale(x int) int {
	return x * base
}
`,
			Quiz: Quiz{
				Prompt:    "Which expression has an exact compile-time value in types.Info?",
				Options:   []string{"2 + 3", "x * base", "Both expressions"},
				Answer:    0,
				Correct:   "Yes. 2 + 3 has exact value 5; x * base has type int but depends on a parameter.",
				Incorrect: "Look for a fact with a Value field. Static type and exact constant value are different properties.",
			},
		},
		{
			ID:          "control-flow",
			Number:      4,
			Title:       "SSA makes flow explicit",
			Kicker:      "x/tools/go/ssa",
			Explanation: "Assignments from two branches become control-flow edges and a merged SSA value. Explore block successors, then select Phi: each edge corresponds to the predecessor at the same index. The source has no phi keyword; the instruction is created by lowering.",
			Stage:       "ssa",
			Source: `package main

func choose(cond bool) int {
	x := 1
	if cond {
		x = 2
	} else {
		x = 3
	}
	return x
}
`,
			Quiz: Quiz{
				Prompt:    "What chooses the value of x after the two branches join?",
				Options:   []string{"A Phi instruction", "A Store instruction", "The parser rewrites return x"},
				Answer:    0,
				Correct:   "Exactly. Phi selects the incoming value corresponding to the predecessor edge.",
				Incorrect: "Follow both branch edges into the join block and inspect its first value-producing instruction.",
			},
		},
	}
}

// Lessons returns a deep copy of the ordered lesson catalog.
func Lessons() []Lesson {
	catalog := lessonCatalog()
	lessons := make([]Lesson, len(catalog))
	copy(lessons, catalog)
	for i := range lessons {
		lessons[i].Quiz.Options = append([]string(nil), catalog[i].Quiz.Options...)
	}
	return lessons
}
