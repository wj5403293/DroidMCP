#!/bin/bash
# Reproducible ARM64 build for all DroidMCP MCP servers. Flags match
# .github/workflows/build.yml and the Makefile so local, CI and release
# binaries are byte-identical when produced from the same commit:
#
#   -trimpath           : remove $GOPATH / build host paths from the binary
#   -ldflags '-s -w'    : strip symbol table + DWARF
#   -ldflags '-buildid=': zero out the link-time random build ID
#
# A SHA256SUMS file is generated alongside the binaries so operators can
# verify what they install.
set -euo pipefail

mkdir -p bin

SERVICES=("filesystem" "github" "scraper" "termux" "network" "clipboard")

for service in "${SERVICES[@]}"; do
    echo "Building $service for ARM64..."
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
        go build \
            -trimpath \
            -ldflags="-s -w -buildid=" \
            -o "bin/droidmcp-$service" \
            "./cmd/$service"
done

( cd bin && sha256sum droidmcp-* > SHA256SUMS )
echo "Wrote bin/SHA256SUMS"

echo "Done."
