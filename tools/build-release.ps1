param([string]$Output = "bin")
$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path $Output | Out-Null
$targets = @(
    @{ OS = "windows"; Arch = "amd64"; Suffix = ".exe" },
    @{ OS = "linux"; Arch = "amd64"; Suffix = "" },
    @{ OS = "linux"; Arch = "arm64"; Suffix = "" }
)
foreach ($target in $targets) {
    $env:GOOS = $target.OS
    $env:GOARCH = $target.Arch
    $filename = "undertow-agent-$($target.OS)-$($target.Arch)$($target.Suffix)"
    go build -trimpath -buildvcs=false -ldflags='-s -w' -o (Join-Path $Output $filename) ./cmd/undertow-agent
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
Remove-Item Env:GOOS, Env:GOARCH
$operatorName = "undertow"
if ($env:OS -eq "Windows_NT") { $operatorName += ".exe" }
$buildVersion = (& git describe --tags --always --dirty 2>$null | Select-Object -First 1)
if (-not $buildVersion) { $buildVersion = "dev" }
$buildCommit = (& git rev-parse --short=12 HEAD 2>$null | Select-Object -First 1)
if (-not $buildCommit) { $buildCommit = "none" }
$operatorLdflags = "-s -w -X main.version=$buildVersion -X main.commit=$buildCommit"
go build -trimpath -buildvcs=false -ldflags $operatorLdflags -o (Join-Path $Output $operatorName) ./cmd/undertow
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$recordedVersion = $buildVersion
if ($buildCommit -ne "none") { $recordedVersion += " ($buildCommit)" }
go run ./tools/agentmanifest $Output $recordedVersion
exit $LASTEXITCODE
