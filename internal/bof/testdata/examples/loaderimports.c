#include "beacon.h"

/* Both direct and indirect DLL$Export imports must remain generic. */
DWORD KERNEL32$GetCurrentProcessId(void);
__declspec(dllimport) DWORD KERNEL32$GetTickCount(void);

void go(char *args, int length) {
    datap parser;
    const unsigned char *raw;
    HMODULE existing, loaded;
    FARPROC proc;
    DWORD (WINAPI *current_pid)(void);
    DWORD pid;

    BeaconDataParse(&parser, args, length);
    raw = (const unsigned char *)BeaconDataPtr(&parser, 4);
    existing = GetModuleHandleA("kernel32.dll");
    loaded = LoadLibraryA("kernel32.dll");
    proc = loaded ? GetProcAddress(loaded, "GetCurrentProcessId") : NULL;
    current_pid = (DWORD (WINAPI *)(void))proc;
    pid = KERNEL32$GetCurrentProcessId();
    if (raw && raw[0] == 1 && existing && loaded && current_pid &&
        current_pid() == pid && KERNEL32$GetTickCount() != 0) {
        BeaconPrintf(0, "loader imports pid=%lu raw=%u remaining=%d\n",
                     (unsigned long)pid, (unsigned)raw[0], BeaconDataLength(&parser));
    } else {
        BeaconPrintf(0x0d, "loader imports failed\n");
    }
    if (loaded) FreeLibrary(loaded);
}
