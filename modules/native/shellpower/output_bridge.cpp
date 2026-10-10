#include "output_bridge.h"

#include <Windows.h>
#include <stdlib.h>

static const undertow_native_api_v1 *g_api = NULL;

void shellpower_set_api(const undertow_native_api_v1 *api)
{
    g_api = api;
}

static int shellpower_vprintf(BOOL error, const wchar_t *format, va_list arguments)
{
    va_list copy;
    wchar_t *wide = NULL;
    char *utf8 = NULL;
    int wide_length;
    int utf8_length;
    size_t offset = 0;

    if (!g_api || !format) return -1;
    va_copy(copy, arguments);
    wide_length = _vscwprintf(format, copy);
    va_end(copy);
    if (wide_length < 0) return -1;

    wide = (wchar_t *)HeapAlloc(GetProcessHeap(), 0, ((size_t)wide_length + 1) * sizeof(wchar_t));
    if (!wide) return -1;
    if (vswprintf_s(wide, (size_t)wide_length + 1, format, arguments) < 0) goto fail;

    utf8_length = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, wide, wide_length, NULL, 0, NULL, NULL);
    if (utf8_length == 0 && wide_length != 0) goto fail;
    utf8 = (char *)HeapAlloc(GetProcessHeap(), 0, (size_t)utf8_length + 1);
    if (!utf8) goto fail;
    if (utf8_length > 0 && WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, wide, wide_length, utf8, utf8_length, NULL, NULL) != utf8_length) goto fail;

    while (offset < (size_t)utf8_length) {
        size_t chunk = (size_t)utf8_length - offset;
        int32_t result;
        if (chunk > 16 * 1024) chunk = 16 * 1024;
        result = error ? g_api->write_error(g_api->context, utf8 + offset, chunk)
                       : g_api->write(g_api->context, utf8 + offset, chunk);
        if (result != 0) goto fail;
        offset += chunk;
    }

    HeapFree(GetProcessHeap(), 0, utf8);
    HeapFree(GetProcessHeap(), 0, wide);
    return wide_length;

fail:
    if (utf8) HeapFree(GetProcessHeap(), 0, utf8);
    if (wide) HeapFree(GetProcessHeap(), 0, wide);
    return -1;
}

int shellpower_printf(const wchar_t *format, ...)
{
    int result;
    va_list arguments;
    va_start(arguments, format);
    result = shellpower_vprintf(FALSE, format, arguments);
    va_end(arguments);
    return result;
}

int shellpower_errorf(const wchar_t *format, ...)
{
    int result;
    va_list arguments;
    va_start(arguments, format);
    result = shellpower_vprintf(TRUE, format, arguments);
    va_end(arguments);
    return result;
}

