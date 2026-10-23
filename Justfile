testdata:
    go run ./... lint --config ./test/testdata/glint.yaml ./test/testdata

build:
    go build ./...

test:
    go test ./...

# Reports wall-clock runtime and cumulative memory allocated, e.g.:
#   just perf lint --config glint.yaml ./some-repo
perf *ARGS:
    #!/usr/bin/env bash
    set -euo pipefail
    bin="$(mktemp -t glint-perf)"
    go build -o "$bin" ./cmd/glint
    GLINT_PERF=1 "$bin" {{ARGS}} > /dev/null
    rm -f "$bin"
