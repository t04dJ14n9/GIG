// Package importer provides type information and package registration for the interpreter.
// This file is the compile-time bridge from Gig's registry to Go's type checker:
// it turns registered ExternalPackage values into synthetic *types.Package values
// so source code can import and type-check external packages.
package importer

import (
	"fmt"
	"go/types"
	"sync"

	"github.com/t04dJ14n9/gig/model/external"
)

// Importer implements types.Importer for registered external packages.
//
// It is used during parsing/type-checking. Runtime external calls are
// dispatched through the host.Environment bridge, not through this importer.
type Importer struct {
	// reg is the package registry to use for resolution.
	reg PackageRegistry

	// packages caches resolved types.Package values.
	packages map[string]*types.Package

	// mutex protects concurrent access to packages.
	mutex sync.RWMutex
}

// NewImporter creates a new Importer for resolving packages from reg.
func NewImporter(reg PackageRegistry) *Importer {
	return &Importer{
		reg:      reg,
		packages: make(map[string]*types.Package),
	}
}

// Import returns the synthetic types.Package for an import path.
//
// Resolution order:
//  1. cached *types.Package from a previous type-check
//  2. registry lookup by full package path, e.g. "encoding/json"
//  3. registry lookup by package name/alias, e.g. "json"
//
// The returned package only needs enough type information for Go type checking.
// The actual Go values remain in the registry/CompiledProgram for runtime use.
func (i *Importer) Import(path string) (*types.Package, error) {
	i.mutex.RLock()
	if pkg, ok := i.packages[path]; ok {
		i.mutex.RUnlock()
		if pkg == nil {
			return nil, fmt.Errorf("package %q not found", path)
		}
		return pkg, nil
	}
	i.mutex.RUnlock()

	// Check if it's a registered external package
	extPkg := i.reg.GetPackageByPath(path)
	if extPkg == nil {
		// Try to find by name (for auto-imported packages)
		extPkg = i.reg.GetPackageByName(path)
		if extPkg == nil {
			return nil, fmt.Errorf("package %q not registered", path)
		}
	}

	// Build types.Package from external package
	pkg := i.buildPackage(extPkg)

	i.mutex.Lock()
	i.packages[path] = pkg
	i.mutex.Unlock()

	return pkg, nil
}

// buildPackage creates a synthetic types.Package from a registered ExternalPackage.
// ExternalObject entries become types.Object entries in the package scope:
// functions become *types.Func, variables become *types.Var, constants become
// *types.Const, and named types are registered with their reflect.Type mapping.
func (i *Importer) buildPackage(extPkg *ExternalPackage) *types.Package {
	pkg := types.NewPackage(extPkg.Path, extPkg.Name)

	// Add all objects to the package scope
	for name, obj := range extPkg.Objects {
		var typesObj types.Object

		switch obj.Kind {
		case external.ObjectKindFunction:
			if sig, ok := obj.Type.(*types.Signature); ok {
				typesObj = types.NewFunc(0, pkg, name, sig)
			}
		case external.ObjectKindVariable:
			typesObj = types.NewVar(0, pkg, name, obj.Type)
		case external.ObjectKindConstant:
			// Create a constant with the appropriate value
			val := convertToConstantValue(obj.Value)
			typesObj = types.NewConst(0, pkg, name, obj.Type, val)
		case external.ObjectKindType:
			// Type names are handled separately
			if named, ok := obj.Type.(*types.Named); ok {
				typesObj = named.Obj()
			} else {
				typeName := types.NewTypeName(0, pkg, name, obj.Type)
				typesObj = typeName
			}
		}

		if typesObj != nil {
			pkg.Scope().Insert(typesObj)
		}
	}

	// Add types
	for name, rt := range extPkg.Types {
		t := convertReflectType(rt)
		var typeName *types.TypeName

		if named, ok := t.(*types.Named); ok {
			typeName = named.Obj()
		} else {
			typeName = types.NewTypeName(0, pkg, name, t)
			// Create a new named type
			t = types.NewNamed(typeName, t, nil)
		}

		pkg.Scope().Insert(typeName)
		i.reg.SetExternalType(t, rt)
	}

	// Mark the package as complete so the type checker can use it
	pkg.MarkComplete()

	return pkg
}
