package tests

// Package tests - known_issue_test.go verifies that the historical known-issues
// fixture does not keep cases that have already moved into passing regression
// coverage.

import (
	_ "embed"
	"strings"
	"testing"
)

//go:embed testdata/known_issues/main.go
var historicalIssueSrc string

func TestKnownIssuesFixtureDoesNotKeepResolvedCases(t *testing.T) {
	exported, err := exportedFunctionNames(historicalIssueSrc)
	if err != nil {
		t.Fatalf("parse known issues fixture: %v", err)
	}
	exportedSet := make(map[string]bool, len(exported))
	for _, name := range exported {
		exportedSet[name] = true
	}

	resolvedCases := []string{
		"JsonEncodeBug8",
		"StrangeSyntax_Bug1_ConvertNilToInterface",
		"StrangeSyntax_Bug2_NilMapAccess",
		"StrangeSyntax_Bug3_NilMapDelete",
		"StrangeSyntax_Bug4_BlankExpression",
		"StrangeSyntax_Bug5_ChannelClosedSend",
		"StrangeSyntax_Bug6_ClosureReturnNil",
		"PanicRecoverBasic_Bug",
		"PanicRecoverWithValue_Bug",
		"DeferRunsOnPanic_Bug",
		"MultipleDefersOnPanic_Bug",
		"NamedReturnPanicRecover_Bug",
		"NestedRecover_Bug",
		"PanicInDefer_Bug",
		"PanicInClosure_Bug",
		"DeferPanicRecoverChain_Bug",
	}
	var found []string
	for _, name := range resolvedCases {
		if exportedSet[name] {
			found = append(found, name)
		}
	}
	if len(found) > 0 {
		t.Fatalf("resolved or separately covered cases still live in known_issues: %s", strings.Join(found, ", "))
	}
}
