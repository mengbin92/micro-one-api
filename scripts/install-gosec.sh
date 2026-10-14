#!/bin/sh
# Keep gosec's compiler export-data reader compatible with the project Go release.
# Build the pinned scanner in an isolated module; application dependencies stay unchanged.
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/tool-versions.env"
target=${1:-}
if [ -z "$target" ]; then
    target=$(go env GOBIN)
    if [ -z "$target" ]; then
        task_gopath=$(go env GOPATH)
        target=${task_gopath%%:*}/bin
    fi
    target=$target/gosec
fi
metadata=$(go version -m "$target" 2>/dev/null || true)
tab=$(printf '\t')
if printf '%s\n' "$metadata" | grep -F "github.com/securego/gosec/v2${tab}${GOSEC_VERSION}${tab}" >/dev/null &&
   printf '%s\n' "$metadata" | grep -F "golang.org/x/tools${tab}${GOSEC_X_TOOLS_VERSION}${tab}" >/dev/null; then
    exit 0
fi
task_dir=$(mktemp -d "${TMPDIR:-/tmp}/micro-one-api-gosec.XXXXXX")
trap 'rm -rf "$task_dir"' EXIT HUP INT TERM
cat > "$task_dir/go.mod" <<EOF
module micro-one-api/tooling/gosec

go 1.27.2

require (
    github.com/securego/gosec/v2 ${GOSEC_VERSION}
    golang.org/x/tools ${GOSEC_X_TOOLS_VERSION}
)
EOF
(cd "$task_dir" && go build -mod=mod -o "$task_dir/gosec" github.com/securego/gosec/v2/cmd/gosec)
mkdir -p "$(dirname -- "$target")"
mv "$task_dir/gosec" "$target"
