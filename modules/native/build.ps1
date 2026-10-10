param([ValidateSet('hello','wininfo','hostcheck','shellpower','all')][string]$Example = 'all')
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
if (-not (Get-Command cl.exe -ErrorAction SilentlyContinue)) { throw 'Run this in an x64 MSVC Developer PowerShell with cl.exe on PATH.' }
$names = if ($Example -eq 'all') { @('hello','wininfo','hostcheck','shellpower') } else { @($Example) }
foreach ($name in $names) {
    $directory = Join-Path $PSScriptRoot $name
    if ($name -eq 'shellpower') {
        & (Join-Path $directory 'build.ps1')
        continue
    }
    $dll = Join-Path $directory "$name.dll"
    Push-Location $directory
    try {
        $libraries = if ($name -eq 'hostcheck') { @('kernel32.lib','advapi32.lib','iphlpapi.lib','ws2_32.lib') } else { @('kernel32.lib') }
        & cl.exe /nologo /LD /MT /O2 /W4 "/I$(Join-Path $root 'sdk\native')" "$name.c" /link "/OUT:$dll" @libraries
        if ($LASTEXITCODE -ne 0) { throw "cl.exe failed for $name" }
        Push-Location $root
        try {
            & go run ./tools/nativepack -dll $dll -out (Join-Path $directory "$name.module") -name $name -version 1.0.0 -description "Undertow native $name example"
            if ($LASTEXITCODE -ne 0) { throw "nativepack failed for $name" }
        } finally { Pop-Location }
    } finally {
        Pop-Location
        foreach ($suffix in @('dll','obj','lib','exp')) {
            $intermediate = Join-Path $directory "$name.$suffix"
            if (Test-Path -LiteralPath $intermediate) { Remove-Item -LiteralPath $intermediate }
        }
    }
}
