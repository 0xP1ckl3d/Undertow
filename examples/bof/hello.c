#include "beacon.h"
__declspec(dllimport) DWORD WINAPI KERNEL32$GetCurrentProcessId(void);
__declspec(dllimport) HANDLE WINAPI KERNEL32$GetProcessHeap(void);
#pragma section(".bofdata", read, write)
__declspec(allocate(".bofdata")) static int run_count = 0;
void go(char *args, int length) {
    DWORD pid;
    (void)args; (void)length;
    pid = KERNEL32$GetCurrentProcessId();
    run_count++;
    BeaconPrintf(0, "hello BOF pid=%lu count=%d\n", (unsigned long)pid, run_count);
    BeaconOutput(0, "output API\n", 11);
}
