package commands

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDumpPrintsSSA(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "program.go")
	if err := os.WriteFile(srcPath, []byte(`package main

func Add(a, b int) int {
	return a + b
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	output, err := captureStdout(func() error {
		return RunDump(flag.NewFlagSet("dump", flag.ContinueOnError), []string{srcPath})
	})
	if err != nil {
		t.Fatalf("RunDump error: %v", err)
	}

	for _, part := range []string{"# Package: main", "# Member Add", "func Add", "return t0"} {
		if !strings.Contains(output, part) {
			t.Fatalf("dump output missing %q\n%s", part, output)
		}
	}
}

func TestRunDumpReadsStdin(t *testing.T) {
	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() {
		os.Stdin = oldStdin
		_ = reader.Close()
	}()
	os.Stdin = reader

	if _, err := writer.WriteString(`package main

func F() int {
	return 1
}
`); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}

	output, err := captureStdout(func() error {
		return RunDump(flag.NewFlagSet("dump", flag.ContinueOnError), []string{"-"})
	})
	if err != nil {
		t.Fatalf("RunDump stdin error: %v", err)
	}
	if !strings.Contains(output, "# Member F") || !strings.Contains(output, "func F() int") {
		t.Fatalf("stdin dump missing function F\n%s", output)
	}
}

func TestRunDumpReadsRawSource(t *testing.T) {
	const src = `package main

type Point struct {
	x, y int
}

func Compute() int {
	p := Point{x: 1, y: 2}
	return p.x * p.y
}
`
	output, err := captureStdout(func() error {
		return RunDump(flag.NewFlagSet("dump", flag.ContinueOnError), []string{"--raw", src})
	})
	if err != nil {
		t.Fatalf("RunDump raw error: %v", err)
	}
	for _, part := range []string{"# Member Compute", "func Compute() int", "Point", "return"} {
		if !strings.Contains(output, part) {
			t.Fatalf("raw dump output missing %q\n%s", part, output)
		}
	}
}

func captureStdout(fn func() error) (string, error) {
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = writer

	runErr := fn()
	closeWriterErr := writer.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, copyErr := io.Copy(&buf, reader)
	closeReaderErr := reader.Close()
	if runErr != nil {
		return "", runErr
	}
	if closeWriterErr != nil {
		return "", closeWriterErr
	}
	if closeReaderErr != nil {
		return "", closeReaderErr
	}
	return buf.String(), copyErr
}
