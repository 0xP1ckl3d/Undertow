$ErrorActionPreference = 'Stop'
if (-not (Get-Command cl.exe -ErrorAction SilentlyContinue)) { throw 'Run in an x64 MSVC Developer PowerShell.' }
Push-Location $PSScriptRoot
try {
    & cl.exe /nologo /LD /MT /O2 /W4 bridge.c /link /OUT:undertow_bof_bridge.dll kernel32.lib
    if ($LASTEXITCODE -ne 0) { throw 'BOF bridge build failed' }
    foreach ($suffix in @('obj','lib','exp')) {
        $path = Join-Path $PSScriptRoot "bridge.$suffix"
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path }
        $path = Join-Path $PSScriptRoot "undertow_bof_bridge.$suffix"
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path }
    }
} finally { Pop-Location }
