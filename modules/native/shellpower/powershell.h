#pragma once
#include "clr.h"

void shCreateConsole();
BOOL shExecScript(LPWSTR pwszScript);

BOOL shDisableEtw(mscorlib::_AppDomain* pAppDomain);
void shPatchAll(mscorlib::_AppDomain* pAppDomain);

BOOL shCreateRunspace(mscorlib::_AppDomain* pAppDomain, VARIANT* pvtRunspaceConfiguration);
BOOL shStartConsole(mscorlib::_AppDomain* pAppDomain, VARIANT vtRunspaceConfiguration, LPCWSTR pwszBanner, LPCWSTR pwszHelp, LPCWSTR* ppwszArguments, DWORD dwArgumentCount);

BOOL shCreate(mscorlib::_AppDomain* pAppDomain, VARIANT* pvtPowerShellInstance);
BOOL shDispose(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance);
BOOL shAddScript(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, LPWSTR pwszScript);
BOOL shAddCommand(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, LPCWSTR pwszCommand);
BOOL shInvoke(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, VARIANT* pvtInvokeResult);
BOOL shBeginInvoke(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, VARIANT* pvtAsyncResult);
BOOL shAsyncCompleted(mscorlib::_AppDomain* pAppDomain, VARIANT vtAsyncResult, PBOOL pbCompleted);
BOOL shEndInvoke(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, VARIANT vtAsyncResult, VARIANT* pvtInvokeResult);
BOOL shStop(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance);
BOOL shReset(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance);
BOOL shGetStream(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, LPCWSTR pwszStreamName, VARIANT* pvtStream);
BOOL shHadErrors(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance, PBOOL pbHadErrors);

void shPrintResult(mscorlib::_AppDomain* pAppDomain, VARIANT vtInvokeResult);
void shPrintResultPlain(mscorlib::_AppDomain* pAppDomain, VARIANT vtInvokeResult);
void shPrintInfo(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance);
void shPrintErrors(mscorlib::_AppDomain* pAppDomain, VARIANT vtPowerShellInstance);
void PrintInformationRecord(mscorlib::_AppDomain* pAppDomain, VARIANT vtInformationRecord);
void PrintErrorRecord(mscorlib::_AppDomain* pAppDomain, VARIANT vtErrorRecord);
void PrintPowerShellInformationStream(mscorlib::_AppDomain* pAppDomain, VARIANT vtInformationStream);
void PrintPowerShellErrorStream(mscorlib::_AppDomain* pAppDomain, VARIANT vtErrorStream);
void PrintPowerShellInvocationStateInfoReason(mscorlib::_AppDomain* pAppDomain, VARIANT vtReason);
void SetConsoleTextColor(WORD wColor, PWORD pwOldColor);
