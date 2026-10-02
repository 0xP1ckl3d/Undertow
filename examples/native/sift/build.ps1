param(
    [string]$UndertowRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path,
    [string]$Output = (Join-Path $PSScriptRoot 'sift.module'),
    [switch]$KeepDll
)
$ErrorActionPreference = 'Stop'

if (-not (Get-Command cl.exe -ErrorAction SilentlyContinue)) {
    throw 'Run this in an x64 MSVC Developer PowerShell with cl.exe on PATH.'
}
if (-not (Get-Command go.exe -ErrorAction SilentlyContinue)) {
    throw 'Go must be on PATH so tools/nativepack can package the module.'
}
if (-not (Get-Command python.exe -ErrorAction SilentlyContinue)) {
    throw 'Python must be on PATH to compile the vendored Sift rules.'
}

$sdk = Join-Path $UndertowRoot 'sdk\native'
$source = Join-Path $PSScriptRoot 'sift.c'
$dll = Join-Path $PSScriptRoot 'sift.dll'
$pcreDir = Join-Path $PSScriptRoot 'pcre2\src'
$pcreFiles = @(
    'pcre2_auto_possess.c','pcre2_chkdint.c','pcre2_chartables.c','pcre2_compile.c',
    'pcre2_compile_cgroup.c','pcre2_compile_class.c','pcre2_config.c','pcre2_context.c',
    'pcre2_convert.c','pcre2_dfa_match.c','pcre2_error.c','pcre2_extuni.c',
    'pcre2_find_bracket.c','pcre2_jit_compile.c','pcre2_maketables.c','pcre2_match.c',
    'pcre2_match_data.c','pcre2_match_next.c','pcre2_newline.c','pcre2_ord2utf.c',
    'pcre2_pattern_info.c','pcre2_script_run.c','pcre2_serialize.c',
    'pcre2_string_utils.c','pcre2_study.c','pcre2_substitute.c','pcre2_substring.c',
    'pcre2_tables.c','pcre2_ucd.c','pcre2_valid_utf.c','pcre2_xclass.c'
) | ForEach-Object { Join-Path $pcreDir $_ }

Push-Location $PSScriptRoot
try {
    & python.exe (Join-Path $PSScriptRoot 'generate_rules.py')
    if ($LASTEXITCODE -ne 0) { throw 'rule generation failed' }

    & cl.exe /nologo /LD /MT /O2 /W3 /utf-8 /DHAVE_CONFIG_H /DSUPPORT_PCRE2_8 /DPCRE2_STATIC /DPCRE2_CODE_UNIT_WIDTH=8 "/I$sdk" "/I$pcreDir" $source $pcreFiles /link "/OUT:$dll" kernel32.lib netapi32.lib
    if ($LASTEXITCODE -ne 0) { throw 'cl.exe failed' }

    Push-Location $UndertowRoot
    try {
        & go run ./tools/nativepack -dll $dll -out $Output -name sift -version 0.2.0 -description 'Sift catalogue sensitive-data discovery for local filesystems and SMB/domain shares'
        if ($LASTEXITCODE -ne 0) { throw 'nativepack failed' }
    } finally {
        Pop-Location
    }

    Write-Host "Built $Output"
} finally {
    Pop-Location
    foreach ($suffix in @('obj','lib','exp')) {
        $p = Join-Path $PSScriptRoot "sift.$suffix"
        if (Test-Path -LiteralPath $p) { Remove-Item -LiteralPath $p -Force }
    }
    if (-not $KeepDll -and (Test-Path -LiteralPath $dll)) { Remove-Item -LiteralPath $dll -Force }
    foreach ($name in $pcreFiles) {
        $p = Join-Path $PSScriptRoot (([IO.Path]::GetFileNameWithoutExtension($name)) + '.obj')
        if (Test-Path -LiteralPath $p) { Remove-Item -LiteralPath $p -Force }
    }
}
