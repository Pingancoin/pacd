#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT"

echo "[1/5] go test ./..."
go test ./...

echo "[2/5] go build ./cmd/pacd ./cmd/pacwallet"
go build ./cmd/pacd ./cmd/pacwallet

echo "[3/5] print mainnet params"
go run ./cmd/pacd --network mainnet --printparams >/tmp/pacd-mainnet-params.txt
cat /tmp/pacd-mainnet-params.txt

echo "[4/5] launch-check mainnet"
go run ./cmd/pacd launch-check --network mainnet

echo "[5/5] verify clean git tree"
if [[ -n "$(git status --short)" ]]; then
  echo "working tree is not clean" >&2
  git status --short
  exit 1
fi

echo "mainnet release check passed"
