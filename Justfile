testdata:
    go run ./... lint --config ./test/testdata/glint.yaml ./test/testdata

build:
    go build ./...

test:
    go test ./...
