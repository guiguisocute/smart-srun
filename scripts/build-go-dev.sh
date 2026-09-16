#!/bin/sh
# Build a manifest-checked development payload. This is not an SDK package or
# a signed release: install it only in the isolated development VM.
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output_dir=${1:-"$project_dir/.codex/go-loop/build/go-dev"}
mkdir -p "$output_dir"
output_dir=$(CDPATH= cd -- "$output_dir" && pwd)
stage_dir=$(mktemp -d "${TMPDIR:-/tmp}/smart-srun-dev.XXXXXX")
trap 'rm -rf -- "$stage_dir"' EXIT HUP INT TERM

revision=$(git -C "$project_dir" rev-parse --short=12 HEAD)
if [ -n "$(git -C "$project_dir" status --porcelain -- core)" ]; then revision="$revision.dirty"; fi
version="0.0.0-dev.$revision"
arch=${GOARCH:-$(go env GOARCH)}
mkdir -p "$stage_dir/usr/bin" "$stage_dir/etc/init.d" \
    "$stage_dir/usr/share/smart-srun"
(
    cd "$project_dir/core"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
        -ldflags "-s -w -X github.com/matthewlu070111/smart-srun/core/internal/cli.Version=$version" \
        -o "$stage_dir/usr/bin/srunnet" ./cmd/srunnet
)
# Normalize text resource line endings when building a Windows checkout.
sed 's/\r$//' "$project_dir/root/etc/init.d/smart_srun" > "$stage_dir/etc/init.d/smart_srun"
cp "$project_dir/doc/school-presets.json" "$stage_dir/usr/share/smart-srun/school-presets.json"
chmod 755 "$stage_dir/usr/bin/srunnet" "$stage_dir/etc/init.d/smart_srun"
chmod 644 "$stage_dir/usr/share/smart-srun/school-presets.json"

(
    cd "$stage_dir"
    sha256sum usr/bin/srunnet etc/init.d/smart_srun usr/share/smart-srun/school-presets.json > manifest.sha256
    payload_bytes=$(wc -c < usr/bin/srunnet)
    payload_bytes=$((payload_bytes + $(wc -c < etc/init.d/smart_srun) + $(wc -c < usr/share/smart-srun/school-presets.json)))
    if [ "$payload_bytes" -gt 10485760 ]; then
        echo "Development payload exceeds the 10 MiB budget: $payload_bytes bytes" >&2
        exit 1
    fi
    printf 'version=%s\narch=%s\ninstalled_payload_bytes=%s\nformat=development-tar-not-sdk-package\n' \
        "$version" "$arch" "$payload_bytes" > build-info.txt
    tar -czf "$output_dir/smart-srun-dev-$arch.tar.gz" \
        manifest.sha256 build-info.txt usr/bin/srunnet etc/init.d/smart_srun \
        usr/share/smart-srun/school-presets.json
    cat build-info.txt
)
sha256sum "$output_dir/smart-srun-dev-$arch.tar.gz"
