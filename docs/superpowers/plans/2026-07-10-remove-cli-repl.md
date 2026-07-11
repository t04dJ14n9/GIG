# Remove Gig CLI REPL Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hard-delete the Gig CLI REPL, its plugin manager, and its terminal dependencies while preserving the remaining CLI and all production-readiness fixes.

**Architecture:** Verify removal through the real CLI entry point in a subprocess, then delete the isolated REPL/plugin packages and their single command edge. Tidy the CLI module, rewrite active documentation around `init`, `gen`, and `dump`, and validate the combined production-readiness branch locally and on GitHub Actions.

**Tech Stack:** Go 1.23.1+, `flag`, Go subprocess tests, Go modules, GitHub Actions, GolangCI-Lint v2.4.0, Gosec v2.27.1, Govulncheck.

## Global Constraints

- Work on `codex/remove-cli-repl`, never directly on `main`.
- Preserve every committed interpreter concurrency and cancellation correction.
- Keep `github.com/t04dJ14n9/gig v1.7.7` in the CLI module.
- Do not retain a REPL compatibility shim, hidden command, plugin loader, or extracted replacement module.
- Do not reduce readability to satisfy lint; prefer ordinary control flow and descriptive names.
- Do not skip tests, soften security gates, or increase timeouts to hide failures.
- Do not commit `.workbuddy` or unrelated user files.
- Keep Go 1.23.1 as the declared compatibility floor.

---

### Task 1: Prove the removed command behavior test-first

**Files:**
- Create: `cmd/gig/main_test.go`
- Modify: `cmd/gig/main.go`
- Delete: `cmd/gig/commands/repl.go`

**Interfaces:**
- Consumes: the existing `main()` command registry and process exit behavior.
- Produces: a CLI where `repl` follows the normal unknown-command path and is absent from usage.

- [ ] **Step 1: Write the failing black-box regression test**

Create `cmd/gig/main_test.go`:

```go
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
```

- [ ] **Step 2: Verify RED**

Run:

```bash
cd cmd/gig
go test -count=1 -run '^TestREPLCommandIsUnavailable$' .
```

Expected: FAIL because the existing `gig repl` command exits successfully after stdin reaches EOF rather than reporting an unknown command.

- [ ] **Step 3: Remove the command cleanly**

In `cmd/gig/main.go`, remove the REPL sentence and command example from the package comment, remove this registry element:

```go
{Name: "repl", Usage: "gig repl", Run: commands.RunREPL},
```

Change the usage title to:

```go
fmt.Fprintln(os.Stderr, "gig - generate Gig dependency packages and inspect interpreted programs")
```

Delete the `REPL:` usage block and delete `cmd/gig/commands/repl.go`.

- [ ] **Step 4: Verify GREEN and the remaining command package**

Run:

```bash
cd cmd/gig
go test -count=1 -run '^Test(REPLCommandIsUnavailable|CLIHelperProcess)$' .
go test -count=1 ./commands
```

Expected: PASS; `init`, `gen`, and `dump` continue compiling and their existing tests pass.

- [ ] **Step 5: Commit the command removal**

```bash
git add cmd/gig/main.go cmd/gig/main_test.go cmd/gig/commands/repl.go
git commit -m "refactor(cli): remove repl command"
```

### Task 2: Delete the REPL implementation and plugin dependency

**Files:**
- Delete: `cmd/gig/repl/completion.go`
- Delete: `cmd/gig/repl/input.go`
- Delete: `cmd/gig/repl/known_packages.go`
- Delete: `cmd/gig/repl/session.go`
- Delete: `cmd/gig/repl/source.go`
- Delete: `cmd/gig/repl/vars.go`
- Delete: `cmd/gig/pluginmgr/load_unix.go`
- Delete: `cmd/gig/pluginmgr/load_windows.go`
- Delete: `cmd/gig/pluginmgr/manager.go`
- Delete: `cmd/gig/pluginmgr/manager_test.go`
- Modify: `cmd/gig/go.mod`
- Modify: `cmd/gig/go.sum`

**Interfaces:**
- Consumes: the command edge removed by Task 1.
- Produces: a CLI module with no REPL/plugin packages and no terminal-line-editing dependency.

- [ ] **Step 1: Delete the now-unreachable packages**

Delete every file listed above with `apply_patch`. Do not retain platform stubs or plugin-cache helpers.

- [ ] **Step 2: Remove the direct dependency and tidy transitives**

Remove this requirement from `cmd/gig/go.mod`:

```go
github.com/peterh/liner v1.2.2
```

Then run:

```bash
cd cmd/gig
go mod tidy
```

Expected: `github.com/peterh/liner` and `github.com/mattn/go-runewidth` disappear from `go.mod` and `go.sum`; Gig remains at v1.7.7.

- [ ] **Step 3: Verify the CLI module and dependency graph**

Run:

```bash
cd cmd/gig
go test -race -count=1 ./...
go list -deps ./... | rg 'peterh/liner|mattn/go-runewidth|cmd/gig/(repl|pluginmgr)' && exit 1 || true
go mod tidy
git diff --exit-code -- go.mod go.sum
```

Expected: tests pass, the dependency audit prints nothing, and a second tidy produces no diff.

- [ ] **Step 4: Commit the implementation and dependency deletion**

```bash
git add cmd/gig/repl cmd/gig/pluginmgr cmd/gig/go.mod cmd/gig/go.sum
git commit -m "refactor(cli): delete repl and plugin packages"
```

### Task 3: Remove stale product documentation

**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/cli-guide.md`
- Modify: `docs/cli-guide_CN.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/ARCHITECTURE_CN.md`
- Modify: `docs/superpowers/specs/2026-07-10-production-readiness-fixes-design.md`
- Modify: `docs/superpowers/plans/2026-07-10-production-readiness-fixes.md`

**Interfaces:**
- Consumes: the remaining `init`, `gen`, and `dump` command behavior.
- Produces: active documentation that no longer advertises a removed feature and historical plans that identify the superseding design.

- [ ] **Step 1: Rewrite the active CLI guides**

Keep installation and the existing `init`/`gen` explanations. Remove every REPL, hot-loading, timeout, plugin-cache, interactive example, and troubleshooting section. Add a concise `gig dump` section documenting:

```text
gig dump program.go
gig dump -
gig dump --raw 'package main; func main() {}'
```

Explain that `dump` reads a file, stdin (`-`), or inline source (`--raw`) and prints readable SSA for debugging. Apply the same command inventory and meaning to the Chinese guide.

- [ ] **Step 2: Update contributor and architecture inventories**

Change `CLAUDE.md`'s `cmd/gig/` inventory to `init`, `gen`, and `dump`. Update both architecture documents so they do not claim `main.go` exposes `repl`.

- [ ] **Step 3: Mark superseded plugin work accurately**

In the production-readiness design, replace the plugin-command scope with a short note linking to `2026-07-10-remove-cli-repl-design.md`. In its implementation plan, add a top-level supersession note and remove plugin-manager verification from the final active checklist/diff commands.

- [ ] **Step 4: Audit user-facing references**

Run:

```bash
rg -n -S '\b(REPL|repl|pluginmgr|plugin manager|peterh/liner|go-runewidth)\b' \
  CLAUDE.md README.md README_CN.md docs cmd/gig \
  --glob '!docs/superpowers/specs/2026-07-10-remove-cli-repl-design.md' \
  --glob '!docs/superpowers/plans/2026-07-10-remove-cli-repl.md' \
  --glob '!docs/superpowers/specs/2026-07-10-production-readiness-fixes-design.md' \
  --glob '!docs/superpowers/plans/2026-07-10-production-readiness-fixes.md' \
  --glob '!cmd/gig/main_test.go'
```

Expected: no output. The four design/history files and the regression test may
refer to removal or supersession, but no active product surface does.

- [ ] **Step 5: Commit the documentation cleanup**

```bash
git add CLAUDE.md docs/cli-guide.md docs/cli-guide_CN.md docs/ARCHITECTURE.md docs/ARCHITECTURE_CN.md docs/superpowers
git commit -m "docs: remove repl and plugin guidance"
```

### Task 4: Verify and publish the complete production-readiness branch

**Files:**
- Verify: `.github/workflows/go.yml`
- Verify: all changed files from `main...HEAD`

**Interfaces:**
- Consumes: Tasks 1-3 and all earlier production-readiness commits.
- Produces: a readable, reviewable pull request whose local and remote gates pass.

- [ ] **Step 1: Format and verify both modules**

Run the exact local release commands documented by the production-readiness plan, including:

```bash
gofmt -w cmd/gig/main_test.go cmd/gig/main.go
go test -count=1 ./...
go test -race -count=1 ./...
(cd cmd/gig && go test -count=1 ./...)
(cd cmd/gig && go test -race -count=1 ./...)
GOTOOLCHAIN=go1.23.1 go test -race -count=1 ./...
(cd cmd/gig && GOTOOLCHAIN=go1.23.1 go test -race -count=1 ./...)
```

Expected: every command passes with no race report.

- [ ] **Step 2: Run lint, security, workflow, module, and build gates**

Run root and CLI GolangCI-Lint v2.4.0, root and CLI Gosec v2.27.1 with
the workflow's production scopes, root and CLI Govulncheck, Actionlint, module
tidiness checks, and Linux/Windows cross-builds exactly as specified in the
production-readiness plan and `.github/workflows/go.yml`.

Expected: every command exits 0 with no actionable finding.

- [ ] **Step 3: Review scope and readability**

Run:

```bash
git status --short
git diff --check
git diff --stat main...HEAD
git diff main...HEAD -- cmd/gig .github README.md gig.go docs
```

Expected: `.workbuddy` is the only unrelated untracked path; the diff contains no REPL implementation, no plugin dependency, no reduced-readability lint rewrite, and no accidental user files.

- [ ] **Step 4: Push and open the pull request**

Push `codex/remove-cli-repl` to the `github` remote and open a draft pull request against `main`. The PR description must summarize interpreter concurrency isolation, cancellable interpreter-owned channel operations, CI/security hardening, the CLI v1.7.7 correction, and complete REPL/plugin removal, plus list the local verification evidence.

- [ ] **Step 5: Drive GitHub Actions green**

Inspect every required PR check with the GitHub connector and `gh`. If a check fails, read the exact log, reproduce the failure locally, add a failing regression where behavior changes, implement the minimal readable fix, rerun the relevant full gate, commit, and push. Repeat until all required checks report success.

- [ ] **Step 6: Final completion audit**

Confirm from current repository and PR state that every success criterion in both design documents is proven. Do not claim completion while any check is queued, in progress, skipped unexpectedly, or failing.
