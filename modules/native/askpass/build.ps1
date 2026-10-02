$ErrorActionPreference = 'Stop'

$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$source = Join-Path $PSScriptRoot 'askpass.c'
$dll = Join-Path $PSScriptRoot 'askpass.dll'
$module = Join-Path $PSScriptRoot 'askpass.module'

function Import-MsvcEnvironment {
    if (Get-Command cl.exe -ErrorAction SilentlyContinue) {
        return
    }

    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
    if (-not (Test-Path -LiteralPath $vswhere)) {
        throw 'cl.exe was not found and vswhere.exe is unavailable. Install Visual Studio/Build Tools with Desktop development with C++.'
    }

    $installation = (& $vswhere -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath | Select-Object -First 1)
    if (-not $installation) {
        throw 'No Visual Studio installation with the x64 MSVC toolchain was found.'
    }

    $vsDevCmd = Join-Path $installation 'Common7\Tools\VsDevCmd.bat'
    if (-not (Test-Path -LiteralPath $vsDevCmd)) {
        throw "VsDevCmd.bat was not found at $vsDevCmd"
    }

    $environment = & cmd.exe /s /c "`"$vsDevCmd`" -arch=amd64 -host_arch=amd64 >nul && set"
    foreach ($line in $environment) {
        if ($line -match '^([^=]+)=(.*)$') {
            [Environment]::SetEnvironmentVariable($matches[1], $matches[2], 'Process')
        }
    }

    if (-not (Get-Command cl.exe -ErrorAction SilentlyContinue)) {
        throw 'MSVC environment initialisation completed but cl.exe is still unavailable.'
    }
}

Import-MsvcEnvironment

if (-not (Get-Command go.exe -ErrorAction SilentlyContinue)) {
    throw 'go.exe was not found. Undertow nativepack requires Go.'
}

Push-Location $PSScriptRoot
try {
    & cl.exe /nologo /LD /MT /O2 /W4 /DUNICODE /D_UNICODE `
        "/I$(Join-Path $root 'sdk\native')" `
        $source `
        /link "/OUT:$dll" kernel32.lib advapi32.lib credui.lib secur32.lib ole32.lib
    if ($LASTEXITCODE -ne 0) {
        throw 'cl.exe failed while building askpass.dll'
    }

    Push-Location $root
    try {
        & go run ./tools/nativepack `
            -dll $dll `
            -out $module `
            -name askpass `
            -version 1.0.1 `
            -description 'Credential prompt with current-user prefill'
        if ($LASTEXITCODE -ne 0) {
            throw 'nativepack failed while building askpass.module'
        }
    }
    finally {
        Pop-Location
    }
}
finally {
    Pop-Location
    foreach ($suffix in @('dll', 'obj', 'lib', 'exp')) {
        $intermediate = Join-Path $PSScriptRoot "askpass.$suffix"
        if (Test-Path -LiteralPath $intermediate) {
            Remove-Item -LiteralPath $intermediate -Force
        }
    }
}

Write-Host "[+] Built $module" -ForegroundColor Green
