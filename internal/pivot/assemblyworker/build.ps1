$ErrorActionPreference = 'Stop'
$compiler = Join-Path $env:SystemRoot 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
if (-not (Test-Path -LiteralPath $compiler)) { throw 'The .NET Framework 4.x x64 compiler is required.' }
$source = Join-Path $PSScriptRoot 'worker.cs'
$output = Join-Path $PSScriptRoot 'worker.exe'
& $compiler /nologo /optimize+ /target:exe /platform:x64 "/out:$output" $source
if ($LASTEXITCODE -ne 0) { throw "Assembly worker compilation failed ($LASTEXITCODE)." }
Write-Output "Built $output"
