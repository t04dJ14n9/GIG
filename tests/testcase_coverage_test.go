package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

type correctnessCoverageSet struct {
	name  string
	src   string
	tests []map[string]testCase
}

func TestCorrectnessCaseCoverageAudit(t *testing.T) {
	sets := []correctnessCoverageSet{
		{name: "advanced", src: advancedSrc, tests: []map[string]testCase{advancedTests}},
		{name: "algorithms", src: algorithmsSrc, tests: []map[string]testCase{algorithmsTests}},
		{name: "arithmetic", src: arithmeticSrc, tests: []map[string]testCase{arithmeticTests}},
		{name: "autowrap", src: autowrapSrc, tests: []map[string]testCase{autowrapTests}},
		{name: "bitwise", src: bitwiseSrc, tests: []map[string]testCase{bitwiseTests}},
		{name: "channels", src: channelsSrc, tests: []map[string]testCase{channelsTests}},
		{name: "closures", src: closuresSrc, tests: []map[string]testCase{closuresTests}},
		{name: "closures_advanced", src: closuresAdvancedSrc, tests: []map[string]testCase{closures_advancedTests}},
		{name: "complex", src: complexSrc, tests: []map[string]testCase{complexTests}},
		{name: "controlflow", src: controlflowSrc, tests: []map[string]testCase{controlflowTests}},
		{name: "cornercases", src: cornercasesSrc, tests: []map[string]testCase{cornercasesTests}},
		{name: "edgecases", src: edgecasesSrc, tests: []map[string]testCase{edgecasesTests}},
		{name: "external", src: externalSrc, tests: []map[string]testCase{externalTests}},
		{name: "functions", src: functionsSrc, tests: []map[string]testCase{functionsTests}},
		{name: "goroutine", src: goroutineSrc, tests: []map[string]testCase{goroutineTests}},
		{name: "initialize", src: initializeSrc, tests: []map[string]testCase{initTests, initializeTests}},
		{name: "leetcode_hard", src: leetcodeHardSrc, tests: []map[string]testCase{leetcode_hardTests}},
		{name: "mapadvanced", src: mapadvancedSrc, tests: []map[string]testCase{mapadvancedTests}},
		{name: "maps", src: mapsSrc, tests: []map[string]testCase{mapsTests}},
		{name: "multiassign", src: multiassignSrc, tests: []map[string]testCase{multiassignTests}},
		{name: "namedreturn", src: namedreturnSrc, tests: []map[string]testCase{namedreturnTests}},
		{name: "panic_recover", src: panicRecoverSrc, tests: []map[string]testCase{panicRecoverTests}},
		{name: "recursion", src: recursionSrc, tests: []map[string]testCase{recursionTests}},
		{name: "resolved_issue", src: resolvedIssueSrc, tests: []map[string]testCase{resolved_issueTests}},
		{name: "scope", src: scopeSrc, tests: []map[string]testCase{scopeTests}},
		{name: "slices", src: slicesSrc, tests: []map[string]testCase{slicesTests}},
		{name: "slicing", src: slicingSrc, tests: []map[string]testCase{slicingTests}},
		{name: "strings_pkg", src: stringsPkgSrc, tests: []map[string]testCase{strings_pkgTests}},
		{name: "structs", src: structsSrc, tests: []map[string]testCase{structsTests}},
		{name: "switch", src: switchSrc, tests: []map[string]testCase{switchTests}},
		{name: "tricky", src: trickySrc, tests: []map[string]testCase{
			trickyClosuresTests,
			trickyDeferTests,
			trickyInterfacesTests,
			trickyMapsTests,
			trickyMultiassignTests,
			trickyNestedTests,
			trickyPointersTests,
			trickySlicesTests,
			trickyStructsTests,
			trickyCoverageTests,
		}},
		{name: "typeconv", src: typeconvSrc, tests: []map[string]testCase{typeconvTests}},
		{name: "variables", src: variablesSrc, tests: []map[string]testCase{variablesTests}},
		{name: "thirdparty/bytes", src: srcBytes, tests: []map[string]testCase{bytesTests}},
		{name: "thirdparty/strings", src: srcStrings, tests: []map[string]testCase{stringsTests}},
		{name: "thirdparty/strconv", src: srcStrconv, tests: []map[string]testCase{strconvTests}},
		{name: "thirdparty/math", src: srcMath, tests: []map[string]testCase{mathTests}},
		{name: "thirdparty/time", src: srcTime, tests: []map[string]testCase{timeTests}},
		{name: "thirdparty/context", src: srcContext, tests: []map[string]testCase{contextTests}},
		{name: "thirdparty/sync", src: srcSync, tests: []map[string]testCase{syncTests}},
		{name: "thirdparty/sort", src: srcSort, tests: []map[string]testCase{sortTests}},
		{name: "thirdparty/encoding", src: srcEncoding, tests: []map[string]testCase{encodingTests}},
		{name: "thirdparty/io", src: srcIO, tests: []map[string]testCase{ioTests}},
		{name: "thirdparty/regexp", src: srcRegexp, tests: []map[string]testCase{regexpTests}},
		{name: "thirdparty/errors", src: srcErrors, tests: []map[string]testCase{errorsTests}},
		{name: "thirdparty/fmt", src: srcFmt, tests: []map[string]testCase{fmtTests}},
		{name: "thirdparty/patterns", src: srcPatterns, tests: []map[string]testCase{patternsTests}},
		{name: "thirdparty/hash", src: srcHash, tests: []map[string]testCase{hashTests}},
		{name: "thirdparty/compress", src: srcCompress, tests: []map[string]testCase{compressTests}},
		{name: "thirdparty/container", src: srcContainer, tests: []map[string]testCase{containerTests}},
		{name: "thirdparty/math_big", src: srcMathBig, tests: []map[string]testCase{mathBigTests}},
		{name: "thirdparty/crypto", src: srcCrypto, tests: []map[string]testCase{cryptoTests}},
		{name: "thirdparty/net_url", src: srcNetURL, tests: []map[string]testCase{netURLTests}},
		{name: "thirdparty/mime", src: srcMime, tests: []map[string]testCase{mimeTests}},
		{name: "thirdparty/text", src: srcText, tests: []map[string]testCase{textTests}},
		{name: "thirdparty_ext/channels", src: srcThirdpartyExtChannels, tests: []map[string]testCase{thirdpartyExtChannelsTests}},
		{name: "thirdparty_ext/closures_defer_types", src: srcThirdpartyExtClosuresDeferTypes, tests: []map[string]testCase{thirdpartyExtClosuresDeferTypesTests}},
		{name: "thirdparty_ext/io_complex", src: srcThirdpartyExtIOComplex, tests: []map[string]testCase{thirdpartyExtIOComplexTests}},
		{name: "thirdparty_ext/json_complex", src: srcThirdpartyExtJSONComplex, tests: []map[string]testCase{thirdpartyExtJSONComplexTests}},
		{name: "thirdparty_ext/math_regexp_sort", src: srcThirdpartyExtMathRegexpSort, tests: []map[string]testCase{thirdpartyExtMathRegexpSortTests}},
		{name: "thirdparty_ext/time_context_sync", src: srcThirdpartyExtTimeContextSync, tests: []map[string]testCase{thirdpartyExtTimeContextSyncTests}},
		{name: "strange_syntax", src: strangeSyntaxSrc, tests: []map[string]testCase{strangeSyntaxTests, strangeSyntaxCoverageTests}},
	}

	for _, set := range sets {
		t.Run(set.name, func(t *testing.T) {
			exported := exportedFunctions(t, set.name, set.src)
			exportedSet := make(map[string]bool, len(exported))
			for _, name := range exported {
				exportedSet[name] = true
			}
			covered := coveredFunctions(set.tests)

			var missing []string
			for _, name := range exported {
				if covered[name] {
					continue
				}
				missing = append(missing, name)
			}
			if len(missing) > 0 {
				t.Fatalf("exported testdata functions without correctness testcase: %s", strings.Join(missing, ", "))
			}

			var unknown []string
			for name := range covered {
				if !exportedSet[name] {
					unknown = append(unknown, name)
				}
			}
			sort.Strings(unknown)
			if len(unknown) > 0 {
				t.Fatalf("correctness testcase references functions not exported by source: %s", strings.Join(unknown, ", "))
			}
		})
	}
}

func exportedFunctions(t *testing.T, name, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name+".go", src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !fn.Name.IsExported() {
			continue
		}
		names = append(names, fn.Name.Name)
	}
	sort.Strings(names)
	return names
}

func coveredFunctions(testMaps []map[string]testCase) map[string]bool {
	covered := make(map[string]bool)
	for _, tests := range testMaps {
		for _, tc := range tests {
			covered[tc.funcName] = true
		}
	}
	return covered
}
