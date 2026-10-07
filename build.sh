#!/bin/sh
# Builds the bridge. -tags goolm uses mautrix's pure-Go encryption, so no C libolm is needed.
set -e
cd "$(dirname "$0")"
go build -tags goolm -ldflags "-X main.Tag=$(git describe --exact-match --tags 2>/dev/null || echo unknown) -X main.Commit=$(git rev-parse HEAD 2>/dev/null || echo unknown) -X 'main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)'" -o upmiigo-bridge ./cmd/upmiigo-bridge
echo "Built ./upmiigo-bridge"
