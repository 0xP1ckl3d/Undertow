#include "beacon.h"
__declspec(dllimport) void WINAPI KERNEL32$Sleep(DWORD);
void go(char *args, int length) {
    (void)args; (void)length;
    BeaconPrintf(0, "BOF loop started\n");
    for (;;) KERNEL32$Sleep(50);
}
