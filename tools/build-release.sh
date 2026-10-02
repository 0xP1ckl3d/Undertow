#!/bin/sh
set -eu
output=${1:-bin}
mkdir -p "$output"
version=$(git describe --tags --always --dirty 2>/dev/null || printf dev)
commit=$(git rev-parse --short=12 HEAD 2>/dev/null || printf none)
for target in windows/amd64 linux/amd64 linux/arm64; do
  platform=${target%/*}
  arch=${target#*/}
  suffix=
  if [ "$platform" = windows ]; then suffix=.exe; fi
  agent_ldflags='-s -w'
  if [ "$platform" = windows ]; then agent_ldflags="$agent_ldflags -H=windowsgui"; fi
  GOOS="$platform" GOARCH="$arch" go build -trimpath -buildvcs=false -ldflags="$agent_ldflags" -o "$output/undertow-agent-$platform-$arch$suffix" ./cmd/undertow-agent
done
go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=$version -X main.commit=$commit" -o "$output/undertow" ./cmd/undertow
recorded_version=$version
if [ "$commit" != none ]; then recorded_version="$version ($commit)"; fi
go run ./tools/agentmanifest "$output" "$recorded_version"
