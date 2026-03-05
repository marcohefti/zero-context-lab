#!/usr/bin/env bash
set -euo pipefail

version="v1.64.8"
cmd=(go run github.com/golangci/golangci-lint/cmd/golangci-lint@"${version}")
"${cmd[@]}" run ./...
