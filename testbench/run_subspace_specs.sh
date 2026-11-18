#!/bin/bash
# Runs the subspace specs of reva: boundary between plain shares and subspaces,
# permission walk, registration, listing and navigation. No instance needed.
#
# Usage:
#   ./run_subspace_specs.sh            # subspace specs only
#   ./run_subspace_specs.sh --all      # whole decomposedfs suite (see README.md for known failures)
#
# Exit code is the exit code of go test.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
GO_BIN="${GO_BIN:-$HOME/go1.26/bin/go}"
if [ ! -x "$GO_BIN" ]; then
  GO_BIN="$(command -v go)"
fi

cd "$REPO_DIR"
echo "go: $("$GO_BIN" version)"

# vendor/ does not match go.mod in this fork, -mod=mod ignores it
if [ "${1:-}" = "--all" ]; then
  "$GO_BIN" test -mod=mod -count=1 ./pkg/storage/pkg/decomposedfs/...
else
  "$GO_BIN" test -mod=mod -count=1 ./pkg/storage/pkg/decomposedfs/node/ -ginkgo.focus="[Ss]ubspace"
  "$GO_BIN" test -mod=mod -count=1 ./pkg/storage/pkg/decomposedfs/ -ginkgo.focus="Subspace"
fi
