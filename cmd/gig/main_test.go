package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const cliHelperEnvironment = "GIG_CLI_HELPER_PROCESS"

func TestREPLCommandIsUnavailable(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=TestCLIHelperProcess", "--", "repl")
	command.Env = append(os.Environ(), cliHelperEnvironment+"=1")

	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf("gig repl exit error = %v, output = %q; want exit status 1", err, output)
	}
	if !strings.Contains(string(output), "Unknown command: repl") {
		t.Fatalf("gig repl output = %q; want unknown-command error", output)
	}
	if strings.Contains(string(output), "gig repl") {
		t.Fatalf("gig repl output still advertises the removed command: %q", output)
	}
}

func TestCLIHelperProcess(t *testing.T) {
	if os.Getenv(cliHelperEnvironment) != "1" {
		return
	}

	separator := 0
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator == 0 {
		t.Fatal("helper process arguments are missing -- separator")
	}

	os.Args = append([]string{"gig"}, os.Args[separator+1:]...)
	main()
}
