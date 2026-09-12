#!/bin/sh
# Go gate for smart-srun 2.0: format, vet, unit tests, coverage, race.
#
# This is the entry point spec 05 fixes by name. It never reports success for
# work it did not run: a missing toolchain or a missing core/ tree is a failure,
# not a skip. Race detection needs cgo, so it is reported as an explicit skip
# with a reason rather than silently dropped.
#
# Usage: scripts/verify-go.sh [--no-race] [packages...]
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
core_dir="$repo_root/core"
want_race=1

while [ $# -gt 0 ]; do
    case "$1" in
        --no-race) want_race=0; shift ;;
        --) shift; break ;;
        *) break ;;
    esac
done
packages=${*:-./...}

fail() { printf 'verify-go: FAIL: %s\n' "$1" >&2; exit 1; }
step() { printf '\n=== %s ===\n' "$1"; }

command -v go >/dev/null 2>&1 || fail "go toolchain not on PATH"
[ -d "$core_dir" ] || fail "core/ does not exist yet; no Go gate can pass (M01 creates it)"
[ -f "$core_dir/go.mod" ] || fail "core/go.mod missing"

cd "$core_dir"
printf 'go: %s\n' "$(go version)"
printf 'core: %s\n' "$core_dir"

step "gofmt"
unformatted=$(gofmt -l . || true)
[ -z "$unformatted" ] || fail "gofmt reports unformatted files:
$unformatted"
echo "ok"

step "go vet"
go vet ./...

step "go test (shuffled, coverage)"
CGO_ENABLED=0 go test -count=1 -shuffle=on -coverprofile=coverage.out $packages

step "coverage summary"
go tool cover -func=coverage.out | tail -n 1

if [ "$want_race" -eq 0 ]; then
    step "race"
    echo "SKIPPED: --no-race requested"
elif ! command -v cc >/dev/null 2>&1 && ! command -v gcc >/dev/null 2>&1; then
    step "race"
    # Spec 05: an unrun gate must say so. Do not let this masquerade as a pass.
    fail "race gate needs a C compiler for CGO_ENABLED=1; none found"
else
    step "go test -race"
    CGO_ENABLED=1 go test -race -count=1 $packages
fi

printf '\nverify-go: all gates passed\n'
