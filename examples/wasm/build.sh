#!/bin/sh
set -eu
if [ "$#" -eq 0 ]; then
  set -- triage privilege-audit inventory artifact-discovery persistence-audit enterprise-posture
fi
for name do
  GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags=-buildid= -o "examples/wasm/$name/$name.wasm" "./examples/wasm/$name"
done
