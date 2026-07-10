package pluginmgr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()

	return &Manager{
		pluginDir: t.TempDir(),
		loaded:    make(map[string]bool),
		registry:  make(map[string]pluginMetadata),
		symbols:   make(map[string]*ExportedSymbols),
	}
}

func TestLoadPackageContextReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := newTestManager(t).LoadPackageContext(ctx, "example.invalid/canceled")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadPackageContext error = %v, want context.Canceled", err)
	}
}

func TestNewManagerUsesFiveMinuteCommandTimeout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if got := NewManager().commandTimeout; got != 5*time.Minute {
		t.Fatalf("commandTimeout = %v, want 5m", got)
	}
}

func TestDownloadPackagePreservesPackageError(t *testing.T) {
	err := newTestManager(t).downloadPackage(context.Background(), "\x00")
	if !errors.Is(err, ErrPackageNotFound) {
		t.Fatalf("downloadPackage error = %v, want ErrPackageNotFound", err)
	}
	if !strings.Contains(err.Error(), "\x00") {
		t.Fatalf("downloadPackage error %q does not contain the package path", err)
	}
}
