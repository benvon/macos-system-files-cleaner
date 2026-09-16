#!/bin/sh
set -eu

unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  printf '%s\n' "Go files need formatting:" "$unformatted" >&2
  exit 1
fi

shellcheck scripts/*.sh
actionlint
goreleaser check
go vet -buildvcs=false ./...
go test -buildvcs=false -race ./...
go build -buildvcs=false -o bin/apfsusage ./cmd/apfsusage
govulncheck ./...
gitleaks dir --no-banner --redact .
if git rev-parse --verify HEAD >/dev/null 2>&1; then
  gitleaks git --no-banner --redact .
else
  printf '%s\n' "Commit-history secret scan skipped: no usable Git HEAD." >&2
fi
