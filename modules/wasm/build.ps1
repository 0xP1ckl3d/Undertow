param([string[]]$Modules = @('triage','privilege-audit','inventory','artifact-discovery','persistence-audit','enterprise-posture'))
$ErrorActionPreference = 'Stop'
$env:GOOS = 'wasip1'
$env:GOARCH = 'wasm'
try {
  foreach ($name in $Modules) {
    go build -trimpath -ldflags='-buildid=' -o "modules/wasm/$name/$name.wasm" "./modules/wasm/$name"
    if ($LASTEXITCODE -ne 0) { throw "build failed: $name" }
  }
} finally {
  Remove-Item Env:GOOS -ErrorAction SilentlyContinue
  Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
}
