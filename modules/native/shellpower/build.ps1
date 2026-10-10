$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
if (-not (Get-Command cl.exe -ErrorAction SilentlyContinue)) {
    throw 'Run this in an x64 MSVC Developer PowerShell with cl.exe on PATH.'
}

$dll = Join-Path $PSScriptRoot 'shellpower.dll'
Push-Location $PSScriptRoot
try {
    & cl.exe /nologo /LD /MT /O2 /W4 /EHsc /DUNICODE /D_UNICODE "/I$(Join-Path $root 'sdk\native')" shellpower.cpp output_bridge.cpp clr.cpp powershell.cpp patch.cpp /link "/OUT:$dll" kernel32.lib mscoree.lib propsys.lib oleaut32.lib
    if ($LASTEXITCODE -ne 0) { throw 'cl.exe failed for shellpower' }
    Push-Location $root
    try {
        & go run ./tools/nativepack -dll $dll -out (Join-Path $PSScriptRoot 'shellpower.module') -name shellpower -version 1.0.0 -description 'In-process Windows PowerShell command and script host'
        if ($LASTEXITCODE -ne 0) { throw 'nativepack failed for shellpower' }
    } finally {
        Pop-Location
    }
} finally {
    Pop-Location
    foreach ($name in @('shellpower.dll','shellpower.exp','shellpower.lib','shellpower.obj','output_bridge.obj','clr.obj','powershell.obj','patch.obj','mscorlib.tlh','mscorlib.tli')) {
        $path = Join-Path $PSScriptRoot $name
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path }
    }
}
