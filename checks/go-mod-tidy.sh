#!/usr/bin/env bash
# go.mod and go.sum must match what `go mod tidy` writes.
#
# A stale go.sum is invisible locally: the build works from whatever the module
# cache already holds. It breaks for someone who fetches the module fresh, and
# it hides which dependencies a release actually needs.
#
# `-diff` reports the change without writing it, so this check never touches the
# working tree. Run `go mod tidy` and commit the result to fix a failure.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

if ! go mod tidy -diff; then
	echo >&2
	echo "go.mod / go.sum are not tidy. Run 'go mod tidy' and commit the result." >&2
	exit 1
fi

echo "go.mod and go.sum are tidy."
