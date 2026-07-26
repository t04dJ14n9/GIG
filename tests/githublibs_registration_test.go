package tests

import (
	"reflect"

	"github.com/google/uuid"
	"github.com/spf13/cast"
	"github.com/t04dJ14n9/gig/importer"
	"github.com/tidwall/gjson"
)

func init() {
	registerGitHubTestPackages()
}

func registerGitHubTestPackages() {
	gjsonPkg := importer.RegisterPackage("github.com/tidwall/gjson", "gjson")
	gjsonPkg.AddFunction("Get", gjson.Get, "")
	gjsonPkg.AddFunction("GetMany", gjson.GetMany, "")
	gjsonPkg.AddFunction("Parse", gjson.Parse, "")
	gjsonPkg.AddType("Result", reflect.TypeOf(gjson.Result{}), "")
	gjsonPkg.AddType("Type", reflect.TypeOf(gjson.Type(0)), "")

	castPkg := importer.RegisterPackage("github.com/spf13/cast", "cast")
	castPkg.AddFunction("ToBool", cast.ToBool, "")
	castPkg.AddFunction("ToIntSlice", cast.ToIntSlice, "")
	castPkg.AddFunction("ToString", cast.ToString, "")
	castPkg.AddFunction("ToStringMapInt", cast.ToStringMapInt, "")
	castPkg.AddFunction("ToStringSlice", cast.ToStringSlice, "")

	uuidPkg := importer.RegisterPackage("github.com/google/uuid", "uuid")
	uuidPkg.AddFunction("MustParse", uuid.MustParse, "")
	uuidPkg.AddFunction("Parse", uuid.Parse, "")
	uuidPkg.AddType("UUID", reflect.TypeOf(uuid.UUID{}), "")
	uuidPkg.AddType("Version", reflect.TypeOf(uuid.Version(0)), "")
}
