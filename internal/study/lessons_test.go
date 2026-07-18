package study

import (
	"context"
	"reflect"
	"testing"
)

func TestLessonsAreCompleteAndCopied(t *testing.T) {
	t.Parallel()

	lessons := Lessons()
	wantIDs := []string{"positions", "precedence", "types", "control-flow"}
	if len(lessons) != len(wantIDs) {
		t.Fatalf("lesson count = %d, want %d", len(lessons), len(wantIDs))
	}
	for i, lesson := range lessons {
		if lesson.ID != wantIDs[i] {
			t.Fatalf("lesson %d ID = %q, want %q", i, lesson.ID, wantIDs[i])
		}
		if lesson.Number != i+1 || lesson.Title == "" || lesson.Explanation == "" || lesson.Stage == "" {
			t.Fatalf("incomplete lesson metadata: %#v", lesson)
		}
		if lesson.Quiz.Answer < 0 || lesson.Quiz.Answer >= len(lesson.Quiz.Options) {
			t.Fatalf("lesson %q has invalid answer %d", lesson.ID, lesson.Quiz.Answer)
		}
		if result := Analyze(context.Background(), lesson.Source); len(result.Diagnostics) != 0 {
			t.Fatalf("lesson %q source is invalid: %#v", lesson.ID, result.Diagnostics)
		}
	}

	lessons[0].Title = "mutated"
	lessons[0].Quiz.Options[0] = "mutated"
	fresh := Lessons()
	if fresh[0].Title == "mutated" || fresh[0].Quiz.Options[0] == "mutated" {
		t.Fatal("Lessons returned mutable package state")
	}
	if reflect.DeepEqual(lessons, fresh) {
		t.Fatal("mutation did not change caller copy")
	}
}
