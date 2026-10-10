#pragma once
#define WIN32_LEAN_AND_MEAN
#include <Windows.h>
#include <stdio.h>
#include "output_bridge.h"

// ShellPower originally wrote to the hosting process console. Native modules
// have no private console, so route all wide output through Undertow's ABI.
#define wprintf shellpower_printf

#define DEBUG TRUE

#define PRINT_INFO(f, ...) wprintf(L"[*] " ## f, __VA_ARGS__);
#define PRINT_WARNING(f, ...) wprintf(L"[!] " ## f, __VA_ARGS__);
#define PRINT_ERROR(f, ...) wprintf(L"[-] " ## f, __VA_ARGS__);
#define EXIT_ON_NULL_POINTER(m, p) if (p == NULL) { PRINT_ERROR("Null pointer: %ws\n", m); goto exit; }

#if DEBUG
#define EXIT_ON_WIN32_ERROR(f, c) if (c) { PRINT_ERROR("Function %ws failed with error code: %d\n", f, GetLastError()); goto exit; }
#define EXIT_ON_HRESULT_ERROR(f, hr) if (FAILED(hr)) { PRINT_ERROR("Function %ws failed with error code: 0x%08x\n", f, hr); goto exit; }
#else
#define EXIT_ON_WIN32_ERROR(f, c) if (c) { PRINT_ERROR("A function failed with error code: %d\n", GetLastError()); goto exit; }
#define EXIT_ON_HRESULT_ERROR(f, hr) if (FAILED(hr)) { PRINT_ERROR("A function failed with error code: 0x%08x\n", hr); goto exit; }
#endif
