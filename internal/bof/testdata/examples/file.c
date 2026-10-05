#include "beacon.h"
__declspec(dllimport) HANDLE WINAPI KERNEL32$GetProcessHeap(void);
__declspec(dllimport) LPVOID WINAPI KERNEL32$HeapAlloc(HANDLE, DWORD, SIZE_T);
__declspec(dllimport) BOOL WINAPI KERNEL32$HeapFree(HANDLE, DWORD, LPVOID);

void go(char *args, int length) {
    const unsigned int size = 100000;
    char open[] = {0, 0, 0, 7, 0, 1, (char)0x86, (char)0xa0, 'p', 'r', 'o', 'o', 'f', '.', 'b', 'i', 'n'};
    char id[] = {0, 0, 0, 7};
    HANDLE heap = KERNEL32$GetProcessHeap();
    char *write = (char *)KERNEL32$HeapAlloc(heap, 0, size + 4);
    unsigned int i;
    (void)args; (void)length;
    if (!write) return;
    for (i = 0; i < 4; i++) write[i] = id[i];
    for (i = 0; i < size; i++) write[4+i] = (char)(i & 0xff);
    BeaconPrintf(0, "file transfer start\n");
    BeaconOutput(2, open, sizeof(open));
    BeaconOutput(8, write, size + 4);
    BeaconOutput(9, id, sizeof(id));
    BeaconPrintf(0, "file transfer complete\n");
    KERNEL32$HeapFree(heap, 0, write);
}
