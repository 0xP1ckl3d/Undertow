#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include "undertow_native.h"

int32_t undertow_main(const undertow_native_api_v1 *api, const void *args, size_t args_len) {
    WCHAR computer[MAX_COMPUTERNAME_LENGTH + 1];
    DWORD computer_len = MAX_COMPUTERNAME_LENGTH + 1;
    SYSTEM_INFO system;
    MEMORYSTATUSEX memory;
    DWORD pid;
    (void)args; (void)args_len;
    if (!api || api->version != UNDERTOW_NATIVE_ABI_VERSION) return 2;
    if (!GetComputerNameW(computer, &computer_len)) {
        api->write_error(api->context, "GetComputerNameW failed\n", 24);
        return 3;
    }
    GetNativeSystemInfo(&system);
    pid = GetCurrentProcessId();
    memory.dwLength = sizeof(memory);
    if (!GlobalMemoryStatusEx(&memory)) return 4;
    if (undertow_native_printf(api, "Windows computer=%ls pid=%lu processors=%lu page_size=%lu memory_mb=%llu\n",
        computer, (unsigned long)pid, (unsigned long)system.dwNumberOfProcessors,
        (unsigned long)system.dwPageSize, (unsigned long long)(memory.ullTotalPhys / (1024 * 1024))) != 0) return 5;
    return api->cancelled(api->context) ? 130 : 0;
}
