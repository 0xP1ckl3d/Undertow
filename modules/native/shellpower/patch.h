#pragma once
#define WIN32_LEAN_AND_MEAN
#include <Windows.h>
#include "clr.h"

BOOL modAmsiOpen();
BOOL modAmsiScan();

BOOL modSysPolicyLock(mscorlib::_AppDomain* pAppDomain);
BOOL modTransFlush(mscorlib::_AppDomain* pAppDomain);
BOOL modAuthRun(mscorlib::_AppDomain* pAppDomain);

BOOL modGetProcAddr(LPCWSTR pwszModuleName, LPCSTR pszProcedureName, PULONG_PTR pProcedureAddress);
BOOL modPatch(LPVOID pTargetAddress, LPBYTE pSourceBuffer, DWORD dwSourceBufferSize);

BOOL modPatchUnmanaged(LPCWSTR pwszMdoduleName, LPCSTR pszProcedureName, LPBYTE pbPatch, DWORD dwPatchSize, DWORD dwPatchOffset);
BOOL modPatchManaged(mscorlib::_AppDomain* pAppDomain, LPCWSTR pwszAssemblyName, LPCWSTR pwszClassName, LPCWSTR pwszMethodName, DWORD dwNbArgs, LPBYTE pbPatch, DWORD dwPatchSize, DWORD dwPatchOffset);

BOOL modFindOffset(LPVOID pStartAddress, LPBYTE pBuffer, DWORD dwBufferSize, DWORD dwMaxSize, PDWORD pdwBufferOffset);

