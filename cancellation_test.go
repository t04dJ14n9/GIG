package gig

import (
	"context"
	"errors"
	"testing"
	"time"
)

const blockingChannelSource = `
func BlockSend() {
	ch := make(chan int)
	ch <- 1
}

func BlockReceive() {
	ch := make(chan int)
	<-ch
}

func BlockSelect() {
	ch := make(chan int)
	select {
	case <-ch:
	}
}

func ReadyReceive() int {
	ch := make(chan int, 1)
	ch <- 7
	return <-ch
}
`

func runWithGuard(t *testing.T, prog *Program, ctx context.Context, name string) error {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		_, err := prog.RunWithContext(ctx, name)
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("%s remained blocked after context cancellation", name)
		return errors.New("unreachable after test failure")
	}
}

func buildBlockingChannelProgram(t *testing.T) *Program {
	t.Helper()

	prog, err := Build(blockingChannelSource)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return prog
}

func assertDeadlineExceeded(t *testing.T, prog *Program, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	err := runWithGuard(t, prog, ctx, name)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%s error = %v, want context.DeadlineExceeded", name, err)
	}
}

func TestRunWithContextCancelsBlockingSend(t *testing.T) {
	assertDeadlineExceeded(t, buildBlockingChannelProgram(t), "BlockSend")
}

func TestRunWithContextCancelsBlockingReceive(t *testing.T) {
	assertDeadlineExceeded(t, buildBlockingChannelProgram(t), "BlockReceive")
}

func TestRunWithContextCancelsBlockingSelect(t *testing.T) {
	assertDeadlineExceeded(t, buildBlockingChannelProgram(t), "BlockSelect")
}

func TestRunWithContextPreservesReadyChannelOperation(t *testing.T) {
	prog := buildBlockingChannelProgram(t)

	value, err := prog.RunWithContext(context.Background(), "ReadyReceive")
	if err != nil {
		t.Fatalf("ReadyReceive: %v", err)
	}
	if value != 7 {
		t.Fatalf("ReadyReceive = %v, want 7", value)
	}
}
