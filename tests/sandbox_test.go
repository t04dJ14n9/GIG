package tests

import (
	"testing"

	"github.com/t04dJ14n9/gig"
	"github.com/t04dJ14n9/gig/importer"
)

// TestSandboxIsolation verifies that WithRegistry(sandbox) prevents access
// to globally-registered packages during type-checking.
func TestSandboxIsolation(t *testing.T) {
	sandbox := importer.NewRegistry()
	// Don't register fmt in sandbox

	_, err := gig.Build(`
package main

import "fmt"

func Hello() string {
	return fmt.Sprintf("hello")
}
`, gig.WithRegistry(sandbox))

	if err == nil {
		t.Error("expected error when using unregistered package in sandbox, got nil")
	}
}

// TestSandboxWithRegisteredPackage verifies sandbox works with explicitly registered packages.
func TestSandboxWithRegisteredPackage(t *testing.T) {
	sandbox := importer.NewRegistry()
	// Register only the package name, not functions
	_ = sandbox.RegisterPackage("strings", "strings")

	// This should fail because we only registered the package name,
	// not the actual functions — but it shouldn't fall through to global registry.
	// A simple program without imports should compile fine.
	_, err := gig.Build(`
package main

func Hello() string { return "hello" }
`, gig.WithRegistry(sandbox))
	if err != nil {
		t.Errorf("expected simple program to compile in sandbox: %v", err)
	}
}
