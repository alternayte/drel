#!/usr/bin/env bash
# Every generated file under examples/ must match what the current emitter
# writes. An emitter change makes every example stale at once, and a stale
# example is the only place a codegen regression shows up as real Go code.
#
# Run it after any change to internal/codegen, then commit the result.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

bin=$(mktemp -d)/drel
trap 'rm -rf "$(dirname "$bin")"' EXIT
go build -o "$bin" ./cmd/drel

while IFS= read -r cfg; do
	dir=$(dirname "$cfg")
	echo "generate: $dir"
	(cd "$dir" && "$bin" generate)
done < <(find examples -name drel.yaml | sort)

if [ -n "$(git status --porcelain -- examples)" ]; then
	echo
	echo "Generated example code is stale:" >&2
	git status --short -- examples >&2
	echo >&2
	echo "Run checks/generated-code-current.sh and commit the result." >&2
	git --no-pager diff -- examples >&2
	exit 1
fi

echo "Generated example code is current."
