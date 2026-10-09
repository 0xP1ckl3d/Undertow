#include "beacon.h"

__declspec(dllimport) HANDLE WINAPI KERNEL32$GetCurrentProcess(void);
__declspec(dllimport) BOOL WINAPI KERNEL32$CloseHandle(HANDLE);
__declspec(dllimport) HLOCAL WINAPI KERNEL32$LocalFree(HLOCAL);
__declspec(dllimport) BOOL WINAPI ADVAPI32$OpenProcessToken(HANDLE, DWORD, PHANDLE);
__declspec(dllimport) BOOL WINAPI ADVAPI32$GetTokenInformation(HANDLE, TOKEN_INFORMATION_CLASS, LPVOID, DWORD, PDWORD);
__declspec(dllimport) BOOL WINAPI ADVAPI32$ConvertSidToStringSidA(PSID, LPSTR *);

void go(char *args, int length) {
    HANDLE token = NULL;
    DWORD needed = 0;
    BYTE buffer[512];
    LPSTR sid = NULL;
    (void)args;
    (void)length;
    if (!ADVAPI32$OpenProcessToken(KERNEL32$GetCurrentProcess(), TOKEN_QUERY, &token)) {
        BeaconPrintf(1, "OpenProcessToken failed\n");
        return;
    }
    if (!ADVAPI32$GetTokenInformation(token, TokenUser, buffer, sizeof(buffer), &needed) ||
        !ADVAPI32$ConvertSidToStringSidA(((TOKEN_USER *)buffer)->User.Sid, &sid)) {
        BeaconPrintf(1, "GetTokenInformation failed\n");
        KERNEL32$CloseHandle(token);
        return;
    }
    BeaconPrintf(0, "BOF token SID=%s\n", sid);
    KERNEL32$LocalFree(sid);
    KERNEL32$CloseHandle(token);
}
