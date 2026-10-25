#!/usr/bin/env bash
# Verifies that the root atla module has no dependencies.
set -euo pipefail

cd "$(dirname "$0")/.."

want="github.com/hirokazumiyaji/atla-go"
got="$(GOWORK=off go list -m all)"
if [ "$got" != "$want" ]; then
	echo "root module must have no dependencies; go list -m all printed:" >&2
	echo "$got" >&2
	exit 1
fi

if grep -E '^\s*require' go.mod; then
	echo "root go.mod must not contain require lines" >&2
	exit 1
fi

echo "ok: root module has no dependencies"
