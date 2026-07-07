package tests

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/t04dJ14n9/gig"
	_ "github.com/t04dJ14n9/gig/stdlib/packages"
	githublibs "github.com/t04dJ14n9/gig/tests/testdata/githublibs"
	thirdparty "github.com/t04dJ14n9/gig/tests/testdata/thirdparty"
	thirdparty_ext "github.com/t04dJ14n9/gig/tests/testdata/thirdparty_ext"
)

// Embed each category source file for interpreter testing.
var (
	//go:embed testdata/thirdparty/bytes.go
	srcBytes string
	//go:embed testdata/thirdparty/strings.go
	srcStrings string
	//go:embed testdata/thirdparty/strconv.go
	srcStrconv string
	//go:embed testdata/thirdparty/math.go
	srcMath string
	//go:embed testdata/thirdparty/time.go
	srcTime string
	//go:embed testdata/thirdparty/context.go
	srcContext string
	//go:embed testdata/thirdparty/sync.go
	srcSync string
	//go:embed testdata/thirdparty/sort.go
	srcSort string
	//go:embed testdata/thirdparty/encoding.go
	srcEncoding string
	//go:embed testdata/thirdparty/io.go
	srcIO string
	// path/filepath removed from sandbox stdlib (OS-specific, uses filesystem)
	// //go:embed testdata/thirdparty/filepath.go
	// srcFilepath string
	//go:embed testdata/thirdparty/regexp.go
	srcRegexp string
	//go:embed testdata/thirdparty/errors.go
	srcErrors string
	//go:embed testdata/thirdparty/fmt.go
	srcFmt string
	//go:embed testdata/thirdparty/patterns.go
	srcPatterns string
	//go:embed testdata/thirdparty/hash.go
	srcHash string
	//go:embed testdata/thirdparty/compress.go
	srcCompress string
	//go:embed testdata/thirdparty/container.go
	srcContainer string
	//go:embed testdata/thirdparty/math_big.go
	srcMathBig string
	//go:embed testdata/thirdparty/crypto.go
	srcCrypto string
	//go:embed testdata/thirdparty/net_url.go
	srcNetURL string
	//go:embed testdata/thirdparty/mime.go
	srcMime string
	//go:embed testdata/thirdparty/text.go
	srcText string
	//go:embed testdata/githublibs/main.go
	srcGithubLibs string
	//go:embed testdata/thirdparty/simple_time.go
	srcSimpleTime string
	//go:embed testdata/thirdparty_ext/channels.go
	srcThirdpartyExtChannels string
	//go:embed testdata/thirdparty_ext/closures_defer_types.go
	srcThirdpartyExtClosuresDeferTypes string
	//go:embed testdata/thirdparty_ext/io_complex.go
	srcThirdpartyExtIOComplex string
	//go:embed testdata/thirdparty_ext/json_complex.go
	srcThirdpartyExtJSONComplex string
	//go:embed testdata/thirdparty_ext/math_regexp_sort.go
	srcThirdpartyExtMathRegexpSort string
	//go:embed testdata/thirdparty_ext/time_context_sync.go
	srcThirdpartyExtTimeContextSync string
	//go:embed testdata/pass_struct_src/pass_bytes_buffer_write_and_read.go
	srcPassBytesBufferWriteAndRead string
	//go:embed testdata/pass_struct_src/pass_bytes_buffer_len.go
	srcPassBytesBufferLen string
	//go:embed testdata/pass_struct_src/pass_bytes_buffer_reset.go
	srcPassBytesBufferReset string
	//go:embed testdata/pass_struct_src/pass_strings_builder.go
	srcPassStringsBuilder string
	//go:embed testdata/pass_struct_src/pass_strings_reader.go
	srcPassStringsReader string
	//go:embed testdata/pass_struct_src/pass_json_encoder.go
	srcPassJsonEncoder string
	//go:embed testdata/pass_struct_src/pass_json_decoder.go
	srcPassJsonDecoder string
	//go:embed testdata/pass_struct_src/pass_csv_writer.go
	srcPassCSVWriter string
	//go:embed testdata/pass_struct_src/pass_gzip_writer.go
	srcPassGzipWriter string
	//go:embed testdata/pass_struct_src/pass_multiple_structs.go
	srcPassMultipleStructs string
	//go:embed testdata/pass_struct_src/pass_and_mutate_bytes_buffer.go
	srcPassAndMutateBytesBuffer string
)

// TestCorrectnessThirdparty tests third-party library calls through the interpreter,
// comparing interpreted results with native Go execution.
func TestCorrectnessThirdparty(t *testing.T) {
	for name, set := range thirdpartyTestSets {
		t.Run(name, func(t *testing.T) {
			runTestSet(t, set)
		})
	}
}

// TestCorrectnessSimpleTime runs simple_time tests (time and context functions
// via inline gig code, compared against hardcoded expected values).
// Unlike thirdparty tests that compare interpreter vs native Go via reflection,
// simple_time tests compare interpreter result against a hardcoded expected value.
// We use a custom runner to avoid callNative attempting to call the expected
// value (int) as a function.
func TestCorrectnessSimpleTime(t *testing.T) {
	prog, err := gig.Build(srcSimpleTime)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for name, tc := range simpleTimeTests {
		t.Run(name, func(t *testing.T) {
			got, err := prog.Run(tc.funcName, tc.args...)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !reflect.DeepEqual(got, tc.native) {
				t.Errorf("got %v (%T), want %v (%T)", got, got, tc.native, tc.native)
			}
		})
	}
}

// TestCorrectnessPassStruct runs host-to-interpreter struct passing tests.
// Each test has a unique source file, so each is compiled and run independently.
// The want field is a hardcoded expected value — there is no native Go
// equivalent for these cross-boundary tests.
func TestCorrectnessPassStruct(t *testing.T) {
	for name, tc := range passStructTests {
		t.Run(name, func(t *testing.T) {
			prog, err := gig.Build(tc.src)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			args := buildPassStructArgs(name, tc.args)
			got, err := prog.Run("Test", args...)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v (%T), want %v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}

// buildPassStructArgs creates the host struct arguments for a pass_struct test.
// The tc.args field carries the "shape" hint (e.g. which types are needed);
// actual initialized values are constructed here.
func buildPassStructArgs(name string, hint []any) []any {
	switch name {
	case "PassBytesBufferWriteAndRead":
		buf := bytes.NewBufferString("prefix: ")
		return []any{buf}
	case "PassBytesBufferLen":
		return []any{bytes.NewBufferString("12345")}
	case "PassBytesBufferReset":
		return []any{bytes.NewBufferString("old stuff")}
	case "PassStringsBuilder":
		b := &strings.Builder{}
		b.WriteString("hello ")
		return []any{b}
	case "PassStringsReader":
		return []any{strings.NewReader("hello gig")}
	case "PassJsonEncoder":
		return []any{json.NewEncoder(&bytes.Buffer{})}
	case "PassJsonDecoder":
		return []any{json.NewDecoder(strings.NewReader(`{"value":99}`))}
	case "PassCSVWriter":
		return []any{csv.NewWriter(&bytes.Buffer{})}
	case "PassGzipWriter":
		return []any{gzip.NewWriter(&bytes.Buffer{})}
	case "PassMultipleStructs":
		return []any{new(bytes.Buffer), json.NewEncoder(&bytes.Buffer{})}
	case "PassAndMutateBytesBuffer":
		return []any{bytes.NewBufferString("host says hi,")}
	default:
		return hint
	}
}

// ============================================================================
// Test registrations — native function references, no hardcoded values
// ============================================================================

var bytesTests = map[string]testCase{
	"BytesBufferWrite":       {srcBytes, "BytesBufferWrite", nil, thirdparty.BytesBufferWrite},
	"BytesBufferWriteString": {srcBytes, "BytesBufferWriteString", nil, thirdparty.BytesBufferWriteString},
	"BytesBufferReadFrom":    {srcBytes, "BytesBufferReadFrom", nil, thirdparty.BytesBufferReadFrom},
	"BytesBufferString":      {srcBytes, "BytesBufferString", nil, thirdparty.BytesBufferString},
	"BytesBufferLen":         {srcBytes, "BytesBufferLen", nil, thirdparty.BytesBufferLen},
	"BytesBufferGrow":        {srcBytes, "BytesBufferGrow", nil, thirdparty.BytesBufferGrow},
	"BytesBufferNext":        {srcBytes, "BytesBufferNext", nil, thirdparty.BytesBufferNext},
	"BytesBufferReadByte":    {srcBytes, "BytesBufferReadByte", nil, thirdparty.BytesBufferReadByte},
	"BytesBufferUnreadByte":  {srcBytes, "BytesBufferUnreadByte", nil, thirdparty.BytesBufferUnreadByte},
	"BytesBufferReadBytes":   {srcBytes, "BytesBufferReadBytes", nil, thirdparty.BytesBufferReadBytes},
	"BytesBufferReadString":  {srcBytes, "BytesBufferReadString", nil, thirdparty.BytesBufferReadString},
	"BytesNewBuffer":         {srcBytes, "BytesNewBuffer", nil, thirdparty.BytesNewBuffer},
	"BytesNewBufferString":   {srcBytes, "BytesNewBufferString", nil, thirdparty.BytesNewBufferString},
	"BytesBufferTrim":        {srcBytes, "BytesBufferTrim", nil, thirdparty.BytesBufferTrim},
	"BytesSplit":             {srcBytes, "BytesSplit", nil, thirdparty.BytesSplit},
	"BytesSplitN":            {srcBytes, "BytesSplitN", nil, thirdparty.BytesSplitN},
	"BytesJoin":              {srcBytes, "BytesJoin", nil, thirdparty.BytesJoin},
	"BytesContains":          {srcBytes, "BytesContains", nil, thirdparty.BytesContains},
	"BytesCount":             {srcBytes, "BytesCount", nil, thirdparty.BytesCount},
	"BytesIndex":             {srcBytes, "BytesIndex", nil, thirdparty.BytesIndex},
	"BytesLastIndex":         {srcBytes, "BytesLastIndex", nil, thirdparty.BytesLastIndex},
	"BytesHasPrefix":         {srcBytes, "BytesHasPrefix", nil, thirdparty.BytesHasPrefix},
	"BytesHasSuffix":         {srcBytes, "BytesHasSuffix", nil, thirdparty.BytesHasSuffix},
	"BytesReplace":           {srcBytes, "BytesReplace", nil, thirdparty.BytesReplace},
	"BytesReplaceAll":        {srcBytes, "BytesReplaceAll", nil, thirdparty.BytesReplaceAll},
	"BytesFields":            {srcBytes, "BytesFields", nil, thirdparty.BytesFields},
	"BytesTrimSpace":         {srcBytes, "BytesTrimSpace", nil, thirdparty.BytesTrimSpace},
	"BytesToUpper":           {srcBytes, "BytesToUpper", nil, thirdparty.BytesToUpper},
	"BytesToLower":           {srcBytes, "BytesToLower", nil, thirdparty.BytesToLower},
	"BytesTrim":              {srcBytes, "BytesTrim", nil, thirdparty.BytesTrim},
	"BytesMap":               {srcBytes, "BytesMap", nil, thirdparty.BytesMap},
}

var stringsTests = map[string]testCase{
	"StringsBuilder":       {srcStrings, "StringsBuilder", nil, thirdparty.StringsBuilder},
	"StringsBuilderString": {srcStrings, "StringsBuilderString", nil, thirdparty.StringsBuilderString},
	"StringsBuilderGrow":   {srcStrings, "StringsBuilderGrow", nil, thirdparty.StringsBuilderGrow},
	"StringsMap":           {srcStrings, "StringsMap", nil, thirdparty.StringsMap},
	"StringsRepeat":        {srcStrings, "StringsRepeat", nil, thirdparty.StringsRepeat},
	"StringsRepeatCount":   {srcStrings, "StringsRepeatCount", nil, thirdparty.StringsRepeatCount},
	"StringsIndexAny":      {srcStrings, "StringsIndexAny", nil, thirdparty.StringsIndexAny},
	"StringsIndexFunc":     {srcStrings, "StringsIndexFunc", nil, thirdparty.StringsIndexFunc},
	"StringsTitle":         {srcStrings, "StringsTitle", nil, thirdparty.StringsTitle},
	"StringsToTitle":       {srcStrings, "StringsToTitle", nil, thirdparty.StringsToTitle},
	"StringsToValidUTF8":   {srcStrings, "StringsToValidUTF8", nil, thirdparty.StringsToValidUTF8},
	"StringsTrimLeft":      {srcStrings, "StringsTrimLeft", nil, thirdparty.StringsTrimLeft},
	"StringsTrimRight":     {srcStrings, "StringsTrimRight", nil, thirdparty.StringsTrimRight},
	"StringsTrimFunc":      {srcStrings, "StringsTrimFunc", nil, thirdparty.StringsTrimFunc},
	"StringsIndexFuncTest": {srcStrings, "StringsIndexFuncTest", nil, thirdparty.StringsIndexFuncTest},
	"StringsCut":           {srcStrings, "StringsCut", nil, thirdparty.StringsCut},
	"StringsCutPrefix":     {srcStrings, "StringsCutPrefix", nil, thirdparty.StringsCutPrefix},
	"StringsCutSuffix":     {srcStrings, "StringsCutSuffix", nil, thirdparty.StringsCutSuffix},
}

var strconvTests = map[string]testCase{
	"StrconvParseBool":    {srcStrconv, "StrconvParseBool", nil, thirdparty.StrconvParseBool},
	"StrconvFormatBool":   {srcStrconv, "StrconvFormatBool", nil, thirdparty.StrconvFormatBool},
	"StrconvParseInt":     {srcStrconv, "StrconvParseInt", nil, thirdparty.StrconvParseInt},
	"StrconvParseUint":    {srcStrconv, "StrconvParseUint", nil, thirdparty.StrconvParseUint},
	"StrconvFormatInt":    {srcStrconv, "StrconvFormatInt", nil, thirdparty.StrconvFormatInt},
	"StrconvFormatUint":   {srcStrconv, "StrconvFormatUint", nil, thirdparty.StrconvFormatUint},
	"StrconvParseFloat":   {srcStrconv, "StrconvParseFloat", nil, thirdparty.StrconvParseFloat},
	"StrconvFormatFloat":  {srcStrconv, "StrconvFormatFloat", nil, thirdparty.StrconvFormatFloat},
	"StrconvQuote":        {srcStrconv, "StrconvQuote", nil, thirdparty.StrconvQuote},
	"StrconvQuoteToASCII": {srcStrconv, "StrconvQuoteToASCII", nil, thirdparty.StrconvQuoteToASCII},
	"StrconvUnquote":      {srcStrconv, "StrconvUnquote", nil, thirdparty.StrconvUnquote},
	"StrconvAppendInt":    {srcStrconv, "StrconvAppendInt", nil, thirdparty.StrconvAppendInt},
	"StrconvAppendFloat":  {srcStrconv, "StrconvAppendFloat", nil, thirdparty.StrconvAppendFloat},
}

var mathTests = map[string]testCase{
	"MathAbs":      {srcMath, "MathAbs", nil, thirdparty.MathAbs},
	"MathMax":      {srcMath, "MathMax", nil, thirdparty.MathMax},
	"MathMin":      {srcMath, "MathMin", nil, thirdparty.MathMin},
	"MathFloor":    {srcMath, "MathFloor", nil, thirdparty.MathFloor},
	"MathCeil":     {srcMath, "MathCeil", nil, thirdparty.MathCeil},
	"MathRound":    {srcMath, "MathRound", nil, thirdparty.MathRound},
	"MathPow":      {srcMath, "MathPow", nil, thirdparty.MathPow},
	"MathSqrt":     {srcMath, "MathSqrt", nil, thirdparty.MathSqrt},
	"MathMod":      {srcMath, "MathMod", nil, thirdparty.MathMod},
	"MathSin":      {srcMath, "MathSin", nil, thirdparty.MathSin},
	"MathCos":      {srcMath, "MathCos", nil, thirdparty.MathCos},
	"MathTan":      {srcMath, "MathTan", nil, thirdparty.MathTan},
	"MathLog":      {srcMath, "MathLog", nil, thirdparty.MathLog},
	"MathLog10":    {srcMath, "MathLog10", nil, thirdparty.MathLog10},
	"MathExp":      {srcMath, "MathExp", nil, thirdparty.MathExp},
	"MathInf":      {srcMath, "MathInf", nil, thirdparty.MathInf},
	"MathNaN":      {srcMath, "MathNaN", nil, thirdparty.MathNaN},
	"MathCopysign": {srcMath, "MathCopysign", nil, thirdparty.MathCopysign},
}

var timeTests = map[string]testCase{
	"TimeNow":       {srcTime, "TimeNow", nil, thirdparty.TimeNow},
	"TimeParse":     {srcTime, "TimeParse", nil, thirdparty.TimeParse},
	"TimeFormat":    {srcTime, "TimeFormat", nil, thirdparty.TimeFormat},
	"TimeAdd":       {srcTime, "TimeAdd", nil, thirdparty.TimeAdd},
	"TimeSub":       {srcTime, "TimeSub", nil, thirdparty.TimeSub},
	"TimeBefore":    {srcTime, "TimeBefore", nil, thirdparty.TimeBefore},
	"TimeAfter":     {srcTime, "TimeAfter", nil, thirdparty.TimeAfter},
	"TimeEqual":     {srcTime, "TimeEqual", nil, thirdparty.TimeEqual},
	"TimeUnix":      {srcTime, "TimeUnix", nil, thirdparty.TimeUnix},
	"TimeUnixMilli": {srcTime, "TimeUnixMilli", nil, thirdparty.TimeUnixMilli},
	"TimeDuration":  {srcTime, "TimeDuration", nil, thirdparty.TimeDuration},
	"TimeSleep":     {srcTime, "TimeSleep", nil, thirdparty.TimeSleep},
}

var contextTests = map[string]testCase{
	"ContextBackground":       {srcContext, "ContextBackground", nil, thirdparty.ContextBackground},
	"ContextTODO":             {srcContext, "ContextTODO", nil, thirdparty.ContextTODO},
	"ContextWithValue":        {srcContext, "ContextWithValue", nil, thirdparty.ContextWithValue},
	"ContextWithCancel":       {srcContext, "ContextWithCancel", nil, thirdparty.ContextWithCancel},
	"ContextWithTimeout":      {srcContext, "ContextWithTimeout", nil, thirdparty.ContextWithTimeout},
	"ContextWithCancelParent": {srcContext, "ContextWithCancelParent", nil, thirdparty.ContextWithCancelParent},
}

var syncTests = map[string]testCase{
	"SyncMutex":          {srcSync, "SyncMutex", nil, thirdparty.SyncMutex},
	"SyncMutexCounter":   {srcSync, "SyncMutexCounter", nil, thirdparty.SyncMutexCounter},
	"SyncRWMutex":        {srcSync, "SyncRWMutex", nil, thirdparty.SyncRWMutex},
	"SyncWaitGroup":      {srcSync, "SyncWaitGroup", nil, thirdparty.SyncWaitGroup},
	"SyncOnce":           {srcSync, "SyncOnce", nil, thirdparty.SyncOnce},
	"SyncOnceFunc":       {srcSync, "SyncOnceFunc", nil, thirdparty.SyncOnceFunc},
	"SyncMap":            {srcSync, "SyncMap", nil, thirdparty.SyncMap},
	"SyncMapLoadOrStore": {srcSync, "SyncMapLoadOrStore", nil, thirdparty.SyncMapLoadOrStore},
}

var sortTests = map[string]testCase{
	"SortStrings":       {srcSort, "SortStrings", nil, thirdparty.SortStrings},
	"SortInts":          {srcSort, "SortInts", nil, thirdparty.SortInts},
	"SortFloat64s":      {srcSort, "SortFloat64s", nil, thirdparty.SortFloat64s},
	"SortSearchInts":    {srcSort, "SortSearchInts", nil, thirdparty.SortSearchInts},
	"SortSearchStrings": {srcSort, "SortSearchStrings", nil, thirdparty.SortSearchStrings},
	"SortSlice":         {srcSort, "SortSlice", nil, thirdparty.SortSlice},
	"SortSliceStable":   {srcSort, "SortSliceStable", nil, thirdparty.SortSliceStable},
	"SortIsSorted":      {srcSort, "SortIsSorted", nil, thirdparty.SortIsSorted},
}

var encodingTests = map[string]testCase{
	"JsonMarshal":       {srcEncoding, "JsonMarshal", nil, thirdparty.JsonMarshal},
	"JsonUnmarshal":     {srcEncoding, "JsonUnmarshal", nil, thirdparty.JsonUnmarshal},
	"JsonMarshalIndent": {srcEncoding, "JsonMarshalIndent", nil, thirdparty.JsonMarshalIndent},
	"JsonDecode":        {srcEncoding, "JsonDecode", nil, thirdparty.JsonDecode},
	"JsonEncode":        {srcEncoding, "JsonEncode", nil, thirdparty.JsonEncode},
	"JsonNumber":        {srcEncoding, "JsonNumber", nil, thirdparty.JsonNumber},
	"Base64Encode":      {srcEncoding, "Base64Encode", nil, thirdparty.Base64Encode},
	"Base64Decode":      {srcEncoding, "Base64Decode", nil, thirdparty.Base64Decode},
	"Base64URLEncode":   {srcEncoding, "Base64URLEncode", nil, thirdparty.Base64URLEncode},
	"Base64URLDecode":   {srcEncoding, "Base64URLDecode", nil, thirdparty.Base64URLDecode},
	"Base64NewEncoder":  {srcEncoding, "Base64NewEncoder", nil, thirdparty.Base64NewEncoder},
	"Base64NewDecoder":  {srcEncoding, "Base64NewDecoder", nil, thirdparty.Base64NewDecoder},
	"HexEncodeToString": {srcEncoding, "HexEncodeToString", nil, thirdparty.HexEncodeToString},
	"HexDecodeString":   {srcEncoding, "HexDecodeString", nil, thirdparty.HexDecodeString},
	"HexNewEncoder":     {srcEncoding, "HexNewEncoder", nil, thirdparty.HexNewEncoder},
	"HexNewDecoder":     {srcEncoding, "HexNewDecoder", nil, thirdparty.HexNewDecoder},
}

var ioTests = map[string]testCase{
	"IoReadAll":       {srcIO, "IoReadAll", nil, thirdparty.IoReadAll},
	"IoCopy":          {srcIO, "IoCopy", nil, thirdparty.IoCopy},
	"IoReadFull":      {srcIO, "IoReadFull", nil, thirdparty.IoReadFull},
	"IoWriteString":   {srcIO, "IoWriteString", nil, thirdparty.IoWriteString},
	"IoSectionReader": {srcIO, "IoSectionReader", nil, thirdparty.IoSectionReader},
	"IoLimitedReader": {srcIO, "IoLimitedReader", nil, thirdparty.IoLimitedReader},
	"IoTeeReader":     {srcIO, "IoTeeReader", nil, thirdparty.IoTeeReader},
}

var regexpTests = map[string]testCase{
	"RegexpMatch":              {srcRegexp, "RegexpMatch", nil, thirdparty.RegexpMatch},
	"RegexpCompile":            {srcRegexp, "RegexpCompile", nil, thirdparty.RegexpCompile},
	"RegexpMustCompile":        {srcRegexp, "RegexpMustCompile", nil, thirdparty.RegexpMustCompile},
	"RegexpFindString":         {srcRegexp, "RegexpFindString", nil, thirdparty.RegexpFindString},
	"RegexpFindStringSubmatch": {srcRegexp, "RegexpFindStringSubmatch", nil, thirdparty.RegexpFindStringSubmatch},
	"RegexpFindAllString":      {srcRegexp, "RegexpFindAllString", nil, thirdparty.RegexpFindAllString},
	"RegexpReplaceAllString":   {srcRegexp, "RegexpReplaceAllString", nil, thirdparty.RegexpReplaceAllString},
	"RegexpSplit":              {srcRegexp, "RegexpSplit", nil, thirdparty.RegexpSplit},
	"RegexpNumSubexp":          {srcRegexp, "RegexpNumSubexp", nil, thirdparty.RegexpNumSubexp},
	"RegexpLongest":            {srcRegexp, "RegexpLongest", nil, thirdparty.RegexpLongest},
}

var errorsTests = map[string]testCase{
	"ErrorsNew":  {srcErrors, "ErrorsNew", nil, thirdparty.ErrorsNew},
	"ErrorsIs":   {srcErrors, "ErrorsIs", nil, thirdparty.ErrorsIs},
	"ErrorsAs":   {srcErrors, "ErrorsAs", nil, thirdparty.ErrorsAs},
	"ErrorsJoin": {srcErrors, "ErrorsJoin", nil, thirdparty.ErrorsJoin},
}

var fmtTests = map[string]testCase{
	"FmtSprintfVarious": {srcFmt, "FmtSprintfVarious", nil, thirdparty.FmtSprintfVarious},
	"FmtSprintfStruct":  {srcFmt, "FmtSprintfStruct", nil, thirdparty.FmtSprintfStruct},
	"FmtSprintfPointer": {srcFmt, "FmtSprintfPointer", nil, thirdparty.FmtSprintfPointer},
	"FmtSprintfBool":    {srcFmt, "FmtSprintfBool", nil, thirdparty.FmtSprintfBool},
	"FmtSprintfHex":     {srcFmt, "FmtSprintfHex", nil, thirdparty.FmtSprintfHex},
	"FmtFprintf":        {srcFmt, "FmtFprintf", nil, thirdparty.FmtFprintf},
	"FmtSprint":         {srcFmt, "FmtSprint", nil, thirdparty.FmtSprint},
	"FmtSprintln":       {srcFmt, "FmtSprintln", nil, thirdparty.FmtSprintln},
	"FmtErrorf":         {srcFmt, "FmtErrorf", nil, thirdparty.FmtErrorf},
}

var patternsTests = map[string]testCase{
	"ChainBytesToStringToBase64":   {srcPatterns, "ChainBytesToStringToBase64", nil, thirdparty.ChainBytesToStringToBase64},
	"ChainStringsBuilderToBuffer":  {srcPatterns, "ChainStringsBuilderToBuffer", nil, thirdparty.ChainStringsBuilderToBuffer},
	"ChainSortSearch":              {srcPatterns, "ChainSortSearch", nil, thirdparty.ChainSortSearch},
	"ChainBufferWriteRead":         {srcPatterns, "ChainBufferWriteRead", nil, thirdparty.ChainBufferWriteRead},
	"ChainContextWithValueChain":   {srcPatterns, "ChainContextWithValueChain", nil, thirdparty.ChainContextWithValueChain},
	"InterfaceWithPointerReceiver": {srcPatterns, "InterfaceWithPointerReceiver", nil, thirdparty.InterfaceWithPointerReceiver},
	"InterfaceSliceOfPointers":     {srcPatterns, "InterfaceSliceOfPointers", nil, thirdparty.InterfaceSliceOfPointers},
	"InterfaceMap":                 {srcPatterns, "InterfaceMap", nil, thirdparty.InterfaceMap},
	"GenericContainerOps":          {srcPatterns, "GenericContainerOps", nil, thirdparty.GenericContainerOps},
	"ReadWithInterface":            {srcPatterns, "ReadWithInterface", nil, thirdparty.ReadWithInterface},
	"WriteWithInterface":           {srcPatterns, "WriteWithInterface", nil, thirdparty.WriteWithInterface},
	"VariadicAppend":               {srcPatterns, "VariadicAppend", nil, thirdparty.VariadicAppend},
	"VariadicStringsJoin":          {srcPatterns, "VariadicStringsJoin", nil, thirdparty.VariadicStringsJoin},
	"VariadicAppendSlice":          {srcPatterns, "VariadicAppendSlice", nil, thirdparty.VariadicAppendSlice},
	"MethodChainingBuilder":        {srcPatterns, "MethodChainingBuilder", nil, thirdparty.MethodChainingBuilder},
	"TableDrivenOp":                {srcPatterns, "TableDrivenOp", nil, thirdparty.TableDrivenOp},
	"FunctionValueFromMap":         {srcPatterns, "FunctionValueFromMap", nil, thirdparty.FunctionValueFromMap},
	"DeferWithMutex":               {srcPatterns, "DeferWithMutex", nil, thirdparty.DeferWithMutex},
	"SelectWithChannels":           {srcPatterns, "SelectWithChannels", nil, thirdparty.SelectWithChannels},
}

var hashTests = map[string]testCase{
	"HashAdler32":      {srcHash, "HashAdler32", nil, thirdparty.HashAdler32},
	"HashAdler32Write": {srcHash, "HashAdler32Write", nil, thirdparty.HashAdler32Write},
	"HashCrc32":        {srcHash, "HashCrc32", nil, thirdparty.HashCrc32},
	"HashCrc32IEEE":    {srcHash, "HashCrc32IEEE", nil, thirdparty.HashCrc32IEEE},
	"HashCrc64ECMA":    {srcHash, "HashCrc64ECMA", nil, thirdparty.HashCrc64ECMA},
	"HashCrc64ISO":     {srcHash, "HashCrc64ISO", nil, thirdparty.HashCrc64ISO},
	"HashFnv64":        {srcHash, "HashFnv64", nil, thirdparty.HashFnv64},
	"HashFnv64a":       {srcHash, "HashFnv64a", nil, thirdparty.HashFnv64a},
}

var compressTests = map[string]testCase{
	"BinaryWriteRead":       {srcCompress, "BinaryWriteRead", nil, thirdparty.BinaryWriteRead},
	"BinaryPutGet":          {srcCompress, "BinaryPutGet", nil, thirdparty.BinaryPutGet},
	"BinaryMultipleValues":  {srcCompress, "BinaryMultipleValues", nil, thirdparty.BinaryMultipleValues},
	"BinaryLittleEndian":    {srcCompress, "BinaryLittleEndian", nil, thirdparty.BinaryLittleEndian},
	"CompressGzipRoundtrip": {srcCompress, "CompressGzipRoundtrip", nil, thirdparty.CompressGzipRoundtrip},
	"CompressZlibRoundtrip": {srcCompress, "CompressZlibRoundtrip", nil, thirdparty.CompressZlibRoundtrip},
}

var containerTests = map[string]testCase{
	"ContainerListPushFrontBack":  {srcContainer, "ContainerListPushFrontBack", nil, thirdparty.ContainerListPushFrontBack},
	"ContainerListRemove":         {srcContainer, "ContainerListRemove", nil, thirdparty.ContainerListRemove},
	"ContainerListMove":           {srcContainer, "ContainerListMove", nil, thirdparty.ContainerListMove},
	"ContainerListIterate":        {srcContainer, "ContainerListIterate", nil, thirdparty.ContainerListIterate},
	"ContainerListReverseIterate": {srcContainer, "ContainerListReverseIterate", nil, thirdparty.ContainerListReverseIterate},
	"ContainerRingSum":            {srcContainer, "ContainerRingSum", nil, thirdparty.ContainerRingSum},
	"ContainerRingMove":           {srcContainer, "ContainerRingMove", nil, thirdparty.ContainerRingMove},
	"ContainerRingLink":           {srcContainer, "ContainerRingLink", nil, thirdparty.ContainerRingLink},
	"ContainerRingUnlink":         {srcContainer, "ContainerRingUnlink", nil, thirdparty.ContainerRingUnlink},
}

var mathBigTests = map[string]testCase{
	"BigIntAdd":        {srcMathBig, "BigIntAdd", nil, thirdparty.BigIntAdd},
	"BigIntMul":        {srcMathBig, "BigIntMul", nil, thirdparty.BigIntMul},
	"BigIntDiv":        {srcMathBig, "BigIntDiv", nil, thirdparty.BigIntDiv},
	"BigIntMod":        {srcMathBig, "BigIntMod", nil, thirdparty.BigIntMod},
	"BigIntPow":        {srcMathBig, "BigIntPow", nil, thirdparty.BigIntPow},
	"BigIntBitwise":    {srcMathBig, "BigIntBitwise", nil, thirdparty.BigIntBitwise},
	"BigIntGCD":        {srcMathBig, "BigIntGCD", nil, thirdparty.BigIntGCD},
	"BigIntPrime":      {srcMathBig, "BigIntPrime", nil, thirdparty.BigIntPrime},
	"BigIntModInverse": {srcMathBig, "BigIntModInverse", nil, thirdparty.BigIntModInverse},
	"BigIntShift":      {srcMathBig, "BigIntShift", nil, thirdparty.BigIntShift},
	"BigIntAbs":        {srcMathBig, "BigIntAbs", nil, thirdparty.BigIntAbs},
	"BigIntString":     {srcMathBig, "BigIntString", nil, thirdparty.BigIntString},
	"BigRatBasic":      {srcMathBig, "BigRatBasic", nil, thirdparty.BigRatBasic},
	"BigRatMul":        {srcMathBig, "BigRatMul", nil, thirdparty.BigRatMul},
	"BigFloatBasic":    {srcMathBig, "BigFloatBasic", nil, thirdparty.BigFloatBasic},
	"BigFloatSqrt":     {srcMathBig, "BigFloatSqrt", nil, thirdparty.BigFloatSqrt},
	"BigFloatExp":      {srcMathBig, "BigFloatExp", nil, thirdparty.BigFloatExp},
}

var cryptoTests = map[string]testCase{
	"CryptoMD5Sum":                    {srcCrypto, "CryptoMD5Sum", nil, thirdparty.CryptoMD5Sum},
	"CryptoMD5Write":                  {srcCrypto, "CryptoMD5Write", nil, thirdparty.CryptoMD5Write},
	"CryptoSHA1Sum":                   {srcCrypto, "CryptoSHA1Sum", nil, thirdparty.CryptoSHA1Sum},
	"CryptoSHA1Write":                 {srcCrypto, "CryptoSHA1Write", nil, thirdparty.CryptoSHA1Write},
	"CryptoSHA256Sum":                 {srcCrypto, "CryptoSHA256Sum", nil, thirdparty.CryptoSHA256Sum},
	"CryptoSHA256Write":               {srcCrypto, "CryptoSHA256Write", nil, thirdparty.CryptoSHA256Write},
	"CryptoSHA512Sum":                 {srcCrypto, "CryptoSHA512Sum", nil, thirdparty.CryptoSHA512Sum},
	"CryptoSHA512_256":                {srcCrypto, "CryptoSHA512_256", nil, thirdparty.CryptoSHA512_256},
	"CryptoHMAC_SHA256":               {srcCrypto, "CryptoHMAC_SHA256", nil, thirdparty.CryptoHMAC_SHA256},
	"CryptoHMACEqual":                 {srcCrypto, "CryptoHMACEqual", nil, thirdparty.CryptoHMACEqual},
	"CryptoHMACDifferent":             {srcCrypto, "CryptoHMACDifferent", nil, thirdparty.CryptoHMACDifferent},
	"CryptoAESEncrypt":                {srcCrypto, "CryptoAESEncrypt", nil, thirdparty.CryptoAESEncrypt},
	"CryptoAESCBC":                    {srcCrypto, "CryptoAESCBC", nil, thirdparty.CryptoAESCBC},
	"CryptoAESOFB":                    {srcCrypto, "CryptoAESOFB", nil, thirdparty.CryptoAESOFB},
	"CryptoSubtleConstantTimeCompare": {srcCrypto, "CryptoSubtleConstantTimeCompare", nil, thirdparty.CryptoSubtleConstantTimeCompare},
	"CryptoSubtleConstantTimeCopy":    {srcCrypto, "CryptoSubtleConstantTimeCopy", nil, thirdparty.CryptoSubtleConstantTimeCopy},
	"CryptoSubtleXORBytes":            {srcCrypto, "CryptoSubtleXORBytes", nil, thirdparty.CryptoSubtleXORBytes},
}

var netURLTests = map[string]testCase{
	"NetURLParse":                    {srcNetURL, "NetURLParse", nil, thirdparty.NetURLParse},
	"NetURLQuery":                    {srcNetURL, "NetURLQuery", nil, thirdparty.NetURLQuery},
	"NetURLQueryEncode":              {srcNetURL, "NetURLQueryEncode", nil, thirdparty.NetURLQueryEncode},
	"NetURLResolveReference":         {srcNetURL, "NetURLResolveReference", nil, thirdparty.NetURLResolveReference},
	"NetURLResolveReferenceRelative": {srcNetURL, "NetURLResolveReferenceRelative", nil, thirdparty.NetURLResolveReferenceRelative},
	"NetURLEscape":                   {srcNetURL, "NetURLEscape", nil, thirdparty.NetURLEscape},
	"NetURLUser":                     {srcNetURL, "NetURLUser", nil, thirdparty.NetURLUser},
	"NetURLString":                   {srcNetURL, "NetURLString", nil, thirdparty.NetURLString},
	"NetNetipParseAddr":              {srcNetURL, "NetNetipParseAddr", nil, thirdparty.NetNetipParseAddr},
	"NetNetipParsePrefix":            {srcNetURL, "NetNetipParsePrefix", nil, thirdparty.NetNetipParsePrefix},
	"NetNetipIPv6":                   {srcNetURL, "NetNetipIPv6", nil, thirdparty.NetNetipIPv6},
	"NetNetipIPv6Full":               {srcNetURL, "NetNetipIPv6Full", nil, thirdparty.NetNetipIPv6Full},
	"NetNetipFrom4":                  {srcNetURL, "NetNetipFrom4", nil, thirdparty.NetNetipFrom4},
	"NetNetipCompare":                {srcNetURL, "NetNetipCompare", nil, thirdparty.NetNetipCompare},
	"NetNetipPrefixContains":         {srcNetURL, "NetNetipPrefixContains", nil, thirdparty.NetNetipPrefixContains},
	"NetNetipMask":                   {srcNetURL, "NetNetipMask", nil, thirdparty.NetNetipMask},
}

var mimeTests = map[string]testCase{
	"MimeTypeByExtension":     {srcMime, "MimeTypeByExtension", nil, thirdparty.MimeTypeByExtension},
	"MimeTypeByExtensionJSON": {srcMime, "MimeTypeByExtensionJSON", nil, thirdparty.MimeTypeByExtensionJSON},
	"MimeExtensionsByType":    {srcMime, "MimeExtensionsByType", nil, thirdparty.MimeExtensionsByType},
	"MimeParseMediaType":      {srcMime, "MimeParseMediaType", nil, thirdparty.MimeParseMediaType},
	"MimeWordEncoder":         {srcMime, "MimeWordEncoder", nil, thirdparty.MimeWordEncoder},
	"MimeWordDecoder":         {srcMime, "MimeWordDecoder", nil, thirdparty.MimeWordDecoder},
	"MultipartFormData":       {srcMime, "MultipartFormData", nil, thirdparty.MultipartFormData},
	"MultipartNewReader":      {srcMime, "MultipartNewReader", nil, thirdparty.MultipartNewReader},
	"MultipartCreate":         {srcMime, "MultipartCreate", nil, thirdparty.MultipartCreate},
}

var textTests = map[string]testCase{
	"TextScannerBasic":    {srcText, "TextScannerBasic", nil, thirdparty.TextScannerBasic},
	"TextScannerInts":     {srcText, "TextScannerInts", nil, thirdparty.TextScannerInts},
	"TextScannerStrings":  {srcText, "TextScannerStrings", nil, thirdparty.TextScannerStrings},
	"TextScannerPosition": {srcText, "TextScannerPosition", nil, thirdparty.TextScannerPosition},
	"TextTabwriterInit":   {srcText, "TextTabwriterInit", nil, thirdparty.TextTabwriterInit},
}

var githubLibsTests = map[string]testCase{
	"GJSONNestedName": {srcGithubLibs, "GJSONNestedName",
		[]any{`{"user":{"profile":{"name":"Ada"}}}`}, githublibs.GJSONNestedName},
	"GJSONManyValues": {srcGithubLibs, "GJSONManyValues",
		[]any{`{"name":"Ada","age":37,"active":true}`}, githublibs.GJSONManyValues},
	"GJSONScoresTotal": {srcGithubLibs, "GJSONScoresTotal",
		[]any{`{"items":[{"score":7},{"score":8},{"score":9}]}`}, githublibs.GJSONScoresTotal},
	"GJSONParseExists": {srcGithubLibs, "GJSONParseExists",
		[]any{`{"meta":{"trace":"abc"}}`}, githublibs.GJSONParseExists},
	"CastToIntSliceTotal":   {srcGithubLibs, "CastToIntSliceTotal", nil, githublibs.CastToIntSliceTotal},
	"CastToStringSliceJoin": {srcGithubLibs, "CastToStringSliceJoin", nil, githublibs.CastToStringSliceJoin},
	"CastMapConversions":    {srcGithubLibs, "CastMapConversions", nil, githublibs.CastMapConversions},
	"CastBoolAndString":     {srcGithubLibs, "CastBoolAndString", nil, githublibs.CastBoolAndString},
	"UUIDParseString": {srcGithubLibs, "UUIDParseString",
		[]any{"f47ac10b-58cc-4372-a567-0e02b2c3d479"}, githublibs.UUIDParseString},
	"UUIDParseVersion": {srcGithubLibs, "UUIDParseVersion",
		[]any{"f47ac10b-58cc-4372-a567-0e02b2c3d479"}, githublibs.UUIDParseVersion},
	"UUIDURN": {srcGithubLibs, "UUIDURN",
		[]any{"f47ac10b-58cc-4372-a567-0e02b2c3d479"}, githublibs.UUIDURN},
}

// simpleTimeTests uses hardcoded expected values (not native comparison)
// since the source is embedded and called via prog.Run.
var simpleTimeTests = map[string]testCase{
	"SimpleTimeSub":      {srcSimpleTime, "SimpleTimeSub", nil, 24},
	"SimpleTimeAdd":      {srcSimpleTime, "SimpleTimeAdd", nil, 2},
	"SimpleContextValue": {srcSimpleTime, "SimpleContextValue", nil, 1},
}

var thirdpartyExtChannelsTests = map[string]testCase{
	"ChannelWithStructElement":    {srcThirdpartyExtChannels, "ChannelWithStructElement", nil, thirdparty_ext.ChannelWithStructElement},
	"ChannelWithInterfaceElement": {srcThirdpartyExtChannels, "ChannelWithInterfaceElement", nil, thirdparty_ext.ChannelWithInterfaceElement},
	"ChannelWithMapElement":       {srcThirdpartyExtChannels, "ChannelWithMapElement", nil, thirdparty_ext.ChannelWithMapElement},
	"ChannelWithSliceElement":     {srcThirdpartyExtChannels, "ChannelWithSliceElement", nil, thirdparty_ext.ChannelWithSliceElement},
	"ChannelWithFuncElement":      {srcThirdpartyExtChannels, "ChannelWithFuncElement", nil, thirdparty_ext.ChannelWithFuncElement},
	"SelectDefaultAlways":         {srcThirdpartyExtChannels, "SelectDefaultAlways", nil, thirdparty_ext.SelectDefaultAlways},
	"SelectNonBlockingSend":       {srcThirdpartyExtChannels, "SelectNonBlockingSend", nil, thirdparty_ext.SelectNonBlockingSend},
	"SelectMultipleChannels":      {srcThirdpartyExtChannels, "SelectMultipleChannels", nil, thirdparty_ext.SelectMultipleChannels},
}

var thirdpartyExtClosuresDeferTypesTests = map[string]testCase{
	"ClosureWithDeferAndPanicRecovery": {srcThirdpartyExtClosuresDeferTypes, "ClosureWithDeferAndPanicRecovery", nil, thirdparty_ext.ClosureWithDeferAndPanicRecovery},
	"ClosureCapturingExternalVar":      {srcThirdpartyExtClosuresDeferTypes, "ClosureCapturingExternalVar", nil, thirdparty_ext.ClosureCapturingExternalVar},
	"MultipleDefersWithExternalCalls":  {srcThirdpartyExtClosuresDeferTypes, "MultipleDefersWithExternalCalls", nil, thirdparty_ext.MultipleDefersWithExternalCalls},
	"TypeSwitchWithExternalTypes":      {srcThirdpartyExtClosuresDeferTypes, "TypeSwitchWithExternalTypes", nil, thirdparty_ext.TypeSwitchWithExternalTypes},
	"TypeSwitchWithMultipleExternalTypes": {srcThirdpartyExtClosuresDeferTypes, "TypeSwitchWithMultipleExternalTypes", nil,
		thirdparty_ext.TypeSwitchWithMultipleExternalTypes},
	"ChainOnInterface": {srcThirdpartyExtClosuresDeferTypes, "ChainOnInterface", nil, thirdparty_ext.ChainOnInterface},
}

var thirdpartyExtIOComplexTests = map[string]testCase{
	"BufferedReader":          {srcThirdpartyExtIOComplex, "BufferedReader", nil, thirdparty_ext.BufferedReader},
	"BufferedWriter":          {srcThirdpartyExtIOComplex, "BufferedWriter", nil, thirdparty_ext.BufferedWriter},
	"MultiWriter":             {srcThirdpartyExtIOComplex, "MultiWriter", nil, thirdparty_ext.MultiWriter},
	"TeeReaderWrapper":        {srcThirdpartyExtIOComplex, "TeeReaderWrapper", nil, thirdparty_ext.TeeReaderWrapper},
	"BytesBufferAsReadWriter": {srcThirdpartyExtIOComplex, "BytesBufferAsReadWriter", nil, thirdparty_ext.BytesBufferAsReadWriter},
	"InterfaceValueNil":       {srcThirdpartyExtIOComplex, "InterfaceValueNil", nil, thirdparty_ext.InterfaceValueNil},
}

var thirdpartyExtJSONComplexTests = map[string]testCase{
	"JsonWithNestedStruct": {srcThirdpartyExtJSONComplex, "JsonWithNestedStruct", nil, thirdparty_ext.JsonWithNestedStruct},
	"JsonWithSliceField":   {srcThirdpartyExtJSONComplex, "JsonWithSliceField", nil, thirdparty_ext.JsonWithSliceField},
	"JsonWithMapField":     {srcThirdpartyExtJSONComplex, "JsonWithMapField", nil, thirdparty_ext.JsonWithMapField},
	"JsonUnmarshalToInterface": {srcThirdpartyExtJSONComplex, "JsonUnmarshalToInterface", nil,
		thirdparty_ext.JsonUnmarshalToInterface},
	"JsonStreamDecoder": {srcThirdpartyExtJSONComplex, "JsonStreamDecoder", nil, thirdparty_ext.JsonStreamDecoder},
}

var thirdpartyExtMathRegexpSortTests = map[string]testCase{
	"MathHypot":             {srcThirdpartyExtMathRegexpSort, "MathHypot", nil, thirdparty_ext.MathHypot},
	"MathAtan2":             {srcThirdpartyExtMathRegexpSort, "MathAtan2", nil, thirdparty_ext.MathAtan2},
	"MathModf":              {srcThirdpartyExtMathRegexpSort, "MathModf", nil, thirdparty_ext.MathModf},
	"MathIsNaNCheck":        {srcThirdpartyExtMathRegexpSort, "MathIsNaNCheck", nil, thirdparty_ext.MathIsNaNCheck},
	"MathIsInfCheck":        {srcThirdpartyExtMathRegexpSort, "MathIsInfCheck", nil, thirdparty_ext.MathIsInfCheck},
	"RegexpLongestMatch":    {srcThirdpartyExtMathRegexpSort, "RegexpLongestMatch", nil, thirdparty_ext.RegexpLongestMatch},
	"RegexpFindAllSubmatch": {srcThirdpartyExtMathRegexpSort, "RegexpFindAllSubmatch", nil, thirdparty_ext.RegexpFindAllSubmatch},
	"RegexpReplaceAllFunc":  {srcThirdpartyExtMathRegexpSort, "RegexpReplaceAllFunc", nil, thirdparty_ext.RegexpReplaceAllFunc},
	"SortWithFunc":          {srcThirdpartyExtMathRegexpSort, "SortWithFunc", nil, thirdparty_ext.SortWithFunc},
	"SortStablePreservingOrder": {srcThirdpartyExtMathRegexpSort, "SortStablePreservingOrder", nil,
		thirdparty_ext.SortStablePreservingOrder},
	"SortFloat64sWithNaN": {srcThirdpartyExtMathRegexpSort, "SortFloat64sWithNaN", nil, thirdparty_ext.SortFloat64sWithNaN},
}

var thirdpartyExtTimeContextSyncTests = map[string]testCase{
	"TimeDurationParse": {srcThirdpartyExtTimeContextSync, "TimeDurationParse", nil, thirdparty_ext.TimeDurationParse},
	"TimeUnixNano":      {srcThirdpartyExtTimeContextSync, "TimeUnixNano", nil, thirdparty_ext.TimeUnixNano},
	"ContextWithCancelAndValue": {srcThirdpartyExtTimeContextSync, "ContextWithCancelAndValue", nil,
		thirdparty_ext.ContextWithCancelAndValue},
	"ContextNestedValues": {srcThirdpartyExtTimeContextSync, "ContextNestedValues", nil, thirdparty_ext.ContextNestedValues},
	"SyncPool":            {srcThirdpartyExtTimeContextSync, "SyncPool", nil, thirdparty_ext.SyncPool},
}

// passStructTestCase describes a host-to-interpreter struct passing test.
type passStructTestCase struct {
	src  string
	args []any
	want any
}

// passStructTests verify passing real Go structs from host into interpreted code.
// Each test has its own source file (unique code per test), so they cannot share
// a single compiled program. The want field is a hardcoded expected value since
// there is no native Go equivalent to compare against.
var passStructTests = map[string]passStructTestCase{
	"PassBytesBufferWriteAndRead": {srcPassBytesBufferWriteAndRead, []any{bytes.NewBufferString("prefix: ")}, "prefix: hello from gig"},
	"PassBytesBufferLen":          {srcPassBytesBufferLen, []any{bytes.NewBufferString("12345")}, 5},
	"PassBytesBufferReset":        {srcPassBytesBufferReset, []any{bytes.NewBufferString("old stuff")}, "new content"},
	"PassStringsBuilder":          {srcPassStringsBuilder, nil, "hello world"},
	"PassStringsReader":           {srcPassStringsReader, []any{strings.NewReader("hello gig")}, 9},
	"PassJsonEncoder":             {srcPassJsonEncoder, []any{json.NewEncoder(&bytes.Buffer{})}, 1},
	"PassJsonDecoder":             {srcPassJsonDecoder, []any{json.NewDecoder(strings.NewReader(`{"value":99}`))}, 99},
	"PassCSVWriter":               {srcPassCSVWriter, []any{csv.NewWriter(&bytes.Buffer{})}, 1},
	"PassGzipWriter":              {srcPassGzipWriter, []any{gzip.NewWriter(&bytes.Buffer{})}, 17},
	"PassMultipleStructs":         {srcPassMultipleStructs, []any{new(bytes.Buffer), json.NewEncoder(&bytes.Buffer{})}, "raw: "},
	"PassAndMutateBytesBuffer":    {srcPassAndMutateBytesBuffer, nil, 26},
}

var thirdpartyTestSets = map[string]testSet{
	"Bytes":                           {src: srcBytes, tests: bytesTests},
	"Strings":                         {src: srcStrings, tests: stringsTests},
	"Strconv":                         {src: srcStrconv, tests: strconvTests},
	"Math":                            {src: srcMath, tests: mathTests},
	"Time":                            {src: srcTime, tests: timeTests},
	"Context":                         {src: srcContext, tests: contextTests},
	"Sync":                            {src: srcSync, tests: syncTests},
	"Sort":                            {src: srcSort, tests: sortTests},
	"Encoding":                        {src: srcEncoding, tests: encodingTests},
	"IO":                              {src: srcIO, tests: ioTests},
	"Regexp":                          {src: srcRegexp, tests: regexpTests},
	"errors":                          {src: srcErrors, tests: errorsTests},
	"Fmt":                             {src: srcFmt, tests: fmtTests},
	"Patterns":                        {src: srcPatterns, tests: patternsTests},
	"Hash":                            {src: srcHash, tests: hashTests},
	"Compress":                        {src: srcCompress, tests: compressTests},
	"Container":                       {src: srcContainer, tests: containerTests},
	"MathBig":                         {src: srcMathBig, tests: mathBigTests},
	"Crypto":                          {src: srcCrypto, tests: cryptoTests},
	"NetURL":                          {src: srcNetURL, tests: netURLTests},
	"Mime":                            {src: srcMime, tests: mimeTests},
	"Text":                            {src: srcText, tests: textTests},
	"GitHubLibraries":                 {src: srcGithubLibs, tests: githubLibsTests},
	"ThirdpartyExtChannels":           {src: srcThirdpartyExtChannels, tests: thirdpartyExtChannelsTests},
	"ThirdpartyExtClosuresDeferTypes": {src: srcThirdpartyExtClosuresDeferTypes, tests: thirdpartyExtClosuresDeferTypesTests, buildOpts: []gig.BuildOption{gig.WithAllowPanic()}},
	"ThirdpartyExtIOComplex":          {src: srcThirdpartyExtIOComplex, tests: thirdpartyExtIOComplexTests},
	"ThirdpartyExtJSONComplex":        {src: srcThirdpartyExtJSONComplex, tests: thirdpartyExtJSONComplexTests},
	"ThirdpartyExtMathRegexpSort":     {src: srcThirdpartyExtMathRegexpSort, tests: thirdpartyExtMathRegexpSortTests},
	"ThirdpartyExtTimeContextSync":    {src: srcThirdpartyExtTimeContextSync, tests: thirdpartyExtTimeContextSyncTests},
}
