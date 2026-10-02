/* Local-only ABI smoke runner for the native DLL before nativepack packaging. */
#define UNICODE
#define _UNICODE
#include <windows.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "undertow_native.h"

static int32_t write_output(uintptr_t context, const void *data, size_t length) {
    FILE *stream = context ? stderr : stdout;
    return fwrite(data, 1, length, stream) == length ? 0 : -1;
}

static int32_t running(uintptr_t context) { (void)context; return 0; }

int wmain(int argc, wchar_t **argv) {
    HMODULE module;
    int32_t (*run)(const undertow_native_api_v1 *, const void *, size_t);
    undertow_native_api_v1 api = {0};
    unsigned char args[65536] = {0};
    size_t cursor = 4;
    int i;
    if (argc < 3) return 2;
    module = LoadLibraryW(argv[1]);
    if (!module) { fprintf(stderr, "LoadLibrary failed: %lu\n", GetLastError()); return 2; }
    run = (int32_t (*)(const undertow_native_api_v1 *, const void *, size_t))GetProcAddress(module, "undertow_main");
    if (!run) return 2;
    *(uint32_t *)args = (uint32_t)(argc - 2);
    for (i = 2; i < argc; ++i) {
        char utf8[8192];
        int n = WideCharToMultiByte(CP_UTF8, 0, argv[i], -1, utf8, sizeof(utf8), NULL, NULL);
        if (n < 1 || cursor + 4 + (size_t)n > sizeof(args)) return 2;
        *(uint32_t *)(args + cursor) = (uint32_t)(n - 1);
        cursor += 4;
        memcpy(args + cursor, utf8, (size_t)n - 1);
        cursor += (size_t)n - 1;
    }
    *(uint32_t *)(args + cursor) = 0;
    cursor += 4;
    api.size = sizeof(api);
    api.version = UNDERTOW_NATIVE_ABI_VERSION;
    api.os = UNDERTOW_NATIVE_OS_WINDOWS;
    api.arch = UNDERTOW_NATIVE_ARCH_AMD64;
    api.write = write_output;
    api.write_error = write_output;
    api.cancelled = running;
    api.job_state = running;
    i = run(&api, args, cursor);
    FreeLibrary(module);
    return i;
}
