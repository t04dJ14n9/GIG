package githublibs

import (
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cast"
	"github.com/tidwall/gjson"
)

// GJSONNestedName tests path lookup and Result.String on tidwall/gjson.
func GJSONNestedName(jsonStr string) string {
	return gjson.Get(jsonStr, "user.profile.name").String()
}

// GJSONManyValues tests variadic GetMany and Result.String.
func GJSONManyValues(jsonStr string) string {
	values := gjson.GetMany(jsonStr, "name", "age", "active")
	return values[0].String() + ":" + values[1].String() + ":" + values[2].String()
}

// GJSONScoresTotal tests Result.Array and Result.Int.
func GJSONScoresTotal(jsonStr string) int64 {
	scores := gjson.Get(jsonStr, "items.#.score").Array()
	var total int64
	for _, score := range scores {
		total += score.Int()
	}
	return total
}

// GJSONParseExists tests Parse, Get, and Exists on Result.
func GJSONParseExists(jsonStr string) bool {
	doc := gjson.Parse(jsonStr)
	return doc.Get("meta.trace").Exists()
}

// CastToIntSliceTotal tests spf13/cast slice conversion from interface values.
func CastToIntSliceTotal() int {
	values := cast.ToIntSlice([]any{"1", 2, 3.9})
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

// CastToStringSliceJoin tests spf13/cast string slice conversion.
func CastToStringSliceJoin() string {
	values := cast.ToStringSlice([]any{"go", 42, true})
	return strings.Join(values, "|")
}

// CastMapConversions tests spf13/cast map conversion.
func CastMapConversions() int {
	values := cast.ToStringMapInt(map[string]any{
		"left":  "20",
		"right": 22,
	})
	return values["left"] + values["right"]
}

// CastBoolAndString tests chained spf13/cast scalar conversions.
func CastBoolAndString() string {
	return cast.ToString(cast.ToBool("true"))
}

// UUIDParseString tests google/uuid Parse and UUID.String.
func UUIDParseString(id string) string {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return ""
	}
	return parsed.String()
}

// UUIDParseVersion tests google/uuid Version on parsed UUID values.
func UUIDParseVersion(id string) int {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return 0
	}
	return int(parsed.Version())
}

// UUIDURN tests google/uuid MustParse and UUID.URN.
func UUIDURN(id string) string {
	return uuid.MustParse(id).URN()
}
