#include "beacon.h"
#include <winternl.h>
__declspec(dllimport) DWORD WINAPI KERNEL32$GetCurrentProcessId(void);
__declspec(dllimport) HANDLE WINAPI KERNEL32$GetCurrentProcess(void);
__declspec(dllimport) BOOL WINAPI KERNEL32$CloseHandle(HANDLE);
__declspec(dllimport) BOOL WINAPI ADVAPI32$OpenProcessToken(HANDLE, DWORD, PHANDLE);
__declspec(dllimport) DWORD WINAPI NETAPI32$NetApiBufferFree(LPVOID);
__declspec(dllimport) ULONG WINAPI IPHLPAPI$GetAdaptersAddresses(ULONG, ULONG, PVOID, PVOID, PULONG);
void go(char *args, int length) {
    HANDLE token = NULL;
    ULONG bytes = 0;
    BOOL opened;
    ULONG adapters;
    DWORD netapi;
    (void)args; (void)length;
    opened = ADVAPI32$OpenProcessToken(KERNEL32$GetCurrentProcess(), TOKEN_QUERY, &token);
    if (opened) KERNEL32$CloseHandle(token);
    adapters = IPHLPAPI$GetAdaptersAddresses(0, 0, NULL, NULL, &bytes);
    netapi = NETAPI32$NetApiBufferFree(NULL);
    BeaconPrintf(0, "imports pid=%lu token=%d adapters=%lu netapi=%lu\n", (unsigned long)KERNEL32$GetCurrentProcessId(), (int)opened, (unsigned long)adapters, (unsigned long)netapi);
}
