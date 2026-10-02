param([string]$UndertowRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path)
$ErrorActionPreference = 'Stop'

if (-not (Get-Command cl.exe -ErrorAction SilentlyContinue)) {
    throw 'Run this in an x64 MSVC Developer PowerShell.'
}

$fixture = Join-Path $PSScriptRoot '.smoke-fixture'
$dll = Join-Path $PSScriptRoot 'sift.dll'
$runner = Join-Path $PSScriptRoot 'smoke.exe'
$sdk = Join-Path $UndertowRoot 'sdk\native'

try {
    & (Join-Path $PSScriptRoot 'build.ps1') -UndertowRoot $UndertowRoot -KeepDll
    Push-Location $PSScriptRoot
    try {
        & cl.exe /nologo /MT /W4 "/I$sdk" (Join-Path $PSScriptRoot 'smoke.c') /link "/OUT:$runner"
        if ($LASTEXITCODE -ne 0) { throw 'smoke runner compilation failed' }
    } finally {
        Pop-Location
    }

    New-Item -ItemType Directory -Path $fixture -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $fixture '.git') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $fixture 'a') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $fixture 'z') -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $fixture 'a\first.env'), 'password="firstSiblingSecret123"')
    [IO.File]::WriteAllText((Join-Path $fixture 'z\last.env'), 'password="lastSiblingSecret456"')
    [IO.File]::WriteAllText((Join-Path $fixture 'config.env'),
        "password=`"syntheticSecret123`"`nAKIA1234567890123456`nghp_abcdefghijklmnopqrstuvwxyz1234567890`naws_secret_access_key=`"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA`"`n")
    [IO.File]::WriteAllText((Join-Path $fixture 'passwords.txt'), 'synthetic fixture')
    [IO.File]::WriteAllText((Join-Path $fixture 'cards.csv'),
        "credit card 4111111111111111`ncredit card 4111111111111112`n")
    [IO.File]::WriteAllBytes((Join-Path $fixture 'utf16.txt'),
        [Text.Encoding]::Unicode.GetPreamble() + [Text.Encoding]::Unicode.GetBytes('api_key="syntheticWideSecret123"'))

    $lines = & $runner $dll local $fixture --json
    if ($LASTEXITCODE -ne 0) { throw "scanner returned $LASTEXITCODE" }
    $records = @($lines | ForEach-Object { $_ | ConvertFrom-Json })
    $findings = @($records | Where-Object type -eq 'finding')
    $summary = $records | Where-Object type -eq 'summary' | Select-Object -Last 1
    if (-not $summary -or $summary.errors -ne 0) { throw 'rule compile or scan errors' }
    if (-not ($findings | Where-Object { $_.rule -eq 'AWS Access Key ID' -and $_.value -eq 'AKIA1234567890123456' })) { throw 'AWS evidence missing' }
    if (-not ($findings | Where-Object { $_.rule -eq 'GitHub Personal Access Token' -and $_.value -eq 'ghp_abcdefghijklmnopqrstuvwxyz1234567890' })) { throw 'GitHub evidence missing' }
    if (-not ($findings | Where-Object { $_.rule -eq 'Generic Secret Assignment' -and $_.value -match 'syntheticSecret123' })) { throw 'full assignment evidence missing' }
    if (@($findings | Where-Object { $_.rule -eq 'Credit Card Number' }).Count -ne 1) { throw 'card validation or deduplication failed' }
    if ($findings | Where-Object { $_.rule -eq 'AWS Secret Key' }) { throw 'entropy filter failed' }
    if (-not ($findings | Where-Object { $_.rule -eq 'Common Password File' })) { throw 'filename rule missing' }
    if (-not ($findings | Where-Object { $_.rule -eq 'Git Repository Directory' })) { throw 'directory rule missing' }
    if (-not ($findings | Where-Object { $_.path -like '*utf16.txt' -and $_.value -match 'syntheticWideSecret123' })) { throw 'UTF-16 evidence missing' }
    if (-not ($findings | Where-Object { $_.path -like '*first.env' -and $_.value -match 'firstSiblingSecret123' })) { throw 'first sibling missing' }
    if (-not ($findings | Where-Object { $_.path -like '*last.env' -and $_.value -match 'lastSiblingSecret456' })) { throw 'last sibling missing' }
    if ($summary.file_limit_hit -or $summary.read_limit_hit -or $summary.depth_limit_hit -or $summary.output_limit_hit) { throw 'unexpected default scan limit' }

    $limited = @(& $runner $dll local $fixture --json --max-files 1 | ForEach-Object { $_ | ConvertFrom-Json })
    if ($LASTEXITCODE -ne 0) { throw 'file-limited scan failed' }
    $limitedSummary = $limited | Where-Object type -eq 'summary' | Select-Object -Last 1
    if (-not $limitedSummary -or $limitedSummary.files -ne 1 -or -not $limitedSummary.file_limit_hit) { throw 'file limit was not reported' }

    $readLimited = @(& $runner $dll local $fixture --json --max-read-mib 0 | ForEach-Object { $_ | ConvertFrom-Json })
    if ($LASTEXITCODE -ne 0) { throw 'read-limited scan failed' }
    $readSummary = $readLimited | Where-Object type -eq 'summary' | Select-Object -Last 1
    if (-not $readSummary -or $readSummary.bytes_read -ne 0 -or -not $readSummary.read_limit_hit) { throw 'read limit was not reported' }
    Write-Host "Sift smoke test passed: $($summary.findings) findings, all upstream patterns compiled."
} finally {
    foreach ($name in @('config.env','passwords.txt','cards.csv','utf16.txt')) {
        $path = Join-Path $fixture $name
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Force }
    }
    foreach ($name in @('a','z')) {
        $dir = Join-Path $fixture $name
        $file = Join-Path $dir $(if ($name -eq 'a') { 'first.env' } else { 'last.env' })
        if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file -Force }
        if (Test-Path -LiteralPath $dir) { Remove-Item -LiteralPath $dir -Force }
    }
    $gitDir = Join-Path $fixture '.git'
    if (Test-Path -LiteralPath $gitDir) { Remove-Item -LiteralPath $gitDir -Force }
    if (Test-Path -LiteralPath $fixture) { Remove-Item -LiteralPath $fixture -Force }
    foreach ($path in @($dll, $runner, (Join-Path $PSScriptRoot 'smoke.obj'))) {
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Force }
    }
}
