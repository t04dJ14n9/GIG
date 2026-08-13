#!/usr/bin/env bash
# Cloud Agent install script for the Gig repository.
#
# Idempotent bootstrap that:
#   1. Refreshes Go module dependencies for every module in the repo.
#   2. Installs the lint/security tools the CI pipeline uses
#      (golangci-lint, gosec) and exposes them on PATH.
#
# Requires the Go toolchain (go1.23.1) to already be present in the base image.
set -euo pipefail

# Versions pinned to match .github/workflows/go.yml.
GOLANGCI_LINT_VERSION="v2.4.0"
GOSEC_VERSION="v2.27.1"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

echo "==> Go toolchain: $(go version)"

# Every module is independent; download deps for all of them.
for module in . cmd/gig examples/simple examples/custom benchmarks; do
  echo "==> go mod download (${module})"
  (cd "${module}" && go mod download)
done

echo "==> Installing golangci-lint ${GOLANGCI_LINT_VERSION}"
go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}"

echo "==> Installing gosec ${GOSEC_VERSION}"
go install "github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION}"

# Expose the Go-installed tools on PATH so bare `golangci-lint` / `gosec`
# invocations (as documented in CLAUDE.md) work. Best-effort: on a snapshot
# these symlinks already exist, so a missing sudo is not fatal.
GOBIN_DIR="$(go env GOPATH)/bin"
if command -v sudo >/dev/null 2>&1; then
  sudo ln -sf "${GOBIN_DIR}/golangci-lint" /usr/local/bin/golangci-lint 2>/dev/null || true
  sudo ln -sf "${GOBIN_DIR}/gosec" /usr/local/bin/gosec 2>/dev/null || true
fi

echo "==> Install complete."
