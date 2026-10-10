#define WIN32_LEAN_AND_MEAN
#include <Windows.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "undertow_native.h"
#include "undertow_native_shell.h"
#include "output_bridge.h"
#include "powershell.h"

static int shell_write(const undertow_native_shell_api_v1 *api, const char *text)
{
    return api->write(api->context, text, strlen(text));
}

static BOOL shell_read_line(const undertow_native_shell_api_v1 *api, char **line_out)
{
    const size_t limit = 64 * 1024;
    char *line = (char *)HeapAlloc(GetProcessHeap(), 0, limit + 1);
    size_t length = 0;
    BOOL escape = FALSE;
    if (!line) return FALSE;
    shell_write(api, "PS> ");
    for (;;) {
        unsigned char value = 0;
        size_t count = 0;
        int32_t read_result = api->read(api->context, &value, 1, &count);
        if (read_result != 0 || count == 0 || api->cancelled(api->context) != 0) {
            HeapFree(GetProcessHeap(), 0, line);
            return FALSE;
        }
        if (escape) {
            if ((value >= '@' && value <= '~') && value != '[') escape = FALSE;
            continue;
        }
        if (value == 0x1b) {
            escape = TRUE;
            continue;
        }
        if (value == 3) {
            length = 0;
            shell_write(api, "^C\r\nPS> ");
            continue;
        }
        if (value == 4 || value == 26) {
            if (length == 0) {
                HeapFree(GetProcessHeap(), 0, line);
                return FALSE;
            }
            continue;
        }
        if (value == 12) {
            shell_write(api, "\x1b[2J\x1b[HPS> ");
            if (length > 0) api->write(api->context, line, length);
            continue;
        }
        if (value == 8 || value == 127) {
            if (length > 0) {
                do { --length; } while (length > 0 && (((unsigned char)line[length] & 0xc0) == 0x80));
                shell_write(api, "\b \b");
            }
            continue;
        }
        if (value == '\r' || value == '\n') {
            shell_write(api, "\r\n");
            line[length] = '\0';
            if (length == 0) {
                shell_write(api, "PS> ");
                continue;
            }
            *line_out = line;
            return TRUE;
        }
        if (value < 0x20 && value != '\t') continue;
        if (length == limit) {
            shell_write(api, "\a");
            continue;
        }
        line[length++] = (char)value;
        api->write(api->context, &value, 1);
    }
}

static BOOL shell_is_exit(const char *line)
{
    while (*line == ' ' || *line == '\t') ++line;
    size_t length = strlen(line);
    while (length > 0 && (line[length - 1] == ' ' || line[length - 1] == '\t')) --length;
    return (length == 4 && _strnicmp(line, "exit", 4) == 0) ||
           (length == 4 && _strnicmp(line, "quit", 4) == 0);
}

static BOOL shell_invoke(const undertow_native_shell_api_v1 *api, mscorlib::_AppDomain *app_domain, VARIANT powershell, wchar_t *script)
{
    VARIANT async_result = { 0 };
    VARIANT invoke_result = { 0 };
    BOOL result = FALSE;
    BOOL had_errors = FALSE;
    BOOL completed = FALSE;
    BOOL stopped = FALSE;

    if (api->cancelled(api->context) != 0) goto exit;
    if (!shAddScript(app_domain, powershell, script)) goto exit;
    if (!shAddCommand(app_domain, powershell, L"Out-String")) goto exit;
    if (!shBeginInvoke(app_domain, powershell, &async_result)) goto exit;
    while (!completed) {
        if (!shAsyncCompleted(app_domain, async_result, &completed)) goto exit;
        if (completed) break;
        if (!stopped && api->cancelled(api->context) != 0) {
            stopped = TRUE;
            shStop(app_domain, powershell);
        }
        Sleep(25);
    }
    if (shEndInvoke(app_domain, powershell, async_result, &invoke_result)) {
        shPrintResultPlain(app_domain, invoke_result);
        shPrintInfo(app_domain, powershell);
        result = TRUE;
    }
    if (shHadErrors(app_domain, powershell, &had_errors) && had_errors) shPrintErrors(app_domain, powershell);
    result = result && !had_errors;

exit:
    VariantClear(&async_result);
    VariantClear(&invoke_result);
    if (!shReset(app_domain, powershell)) result = FALSE;
    return result;
}

static wchar_t *utf8_to_wide(const uint8_t *bytes, size_t length)
{
    const uint8_t *input = bytes;
    size_t input_length = length;
    wchar_t *wide;
    int count;

    if (input_length >= 3 && input[0] == 0xef && input[1] == 0xbb && input[2] == 0xbf) {
        input += 3;
        input_length -= 3;
    }
    if (input_length > INT_MAX) return NULL;
    count = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, (LPCCH)input, (int)input_length, NULL, 0);
    if (count == 0 && input_length != 0) return NULL;
    wide = (wchar_t *)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, ((size_t)count + 1) * sizeof(wchar_t));
    if (!wide) return NULL;
    if (count > 0 && MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, (LPCCH)input, (int)input_length, wide, count) != count) {
        HeapFree(GetProcessHeap(), 0, wide);
        return NULL;
    }
    for (int i = 0; i < count; ++i) {
        if (wide[i] == L'\0') {
            HeapFree(GetProcessHeap(), 0, wide);
            return NULL;
        }
    }
    return wide;
}

static wchar_t *utf16le_to_wide(const uint8_t *bytes, size_t length)
{
    wchar_t *wide;
    size_t count;
    if (length < 2 || bytes[0] != 0xff || bytes[1] != 0xfe || ((length - 2) & 1) != 0) return NULL;
    count = (length - 2) / 2;
    wide = (wchar_t *)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, (count + 1) * sizeof(wchar_t));
    if (!wide) return NULL;
    memcpy(wide, bytes + 2, count * sizeof(wchar_t));
    for (size_t i = 0; i < count; ++i) {
        if (wide[i] == L'\0') {
            HeapFree(GetProcessHeap(), 0, wide);
            return NULL;
        }
    }
    return wide;
}

static wchar_t *script_from_args(const void *args, size_t args_len, uint32_t count)
{
    size_t utf8_length = 0;
    uint8_t *utf8;
    size_t offset = 0;
    undertow_native_span span;

    for (uint32_t i = 0; i < count; ++i) {
        if (undertow_native_arg(args, args_len, i, &span) != 0) return NULL;
        if (SIZE_MAX - utf8_length < span.length + (i == 0 ? 0 : 1)) return NULL;
        utf8_length += span.length + (i == 0 ? 0 : 1);
    }
    utf8 = (uint8_t *)HeapAlloc(GetProcessHeap(), 0, utf8_length == 0 ? 1 : utf8_length);
    if (!utf8) return NULL;
    for (uint32_t i = 0; i < count; ++i) {
        if (undertow_native_arg(args, args_len, i, &span) != 0) {
            HeapFree(GetProcessHeap(), 0, utf8);
            return NULL;
        }
        if (i != 0) utf8[offset++] = ' ';
        memcpy(utf8 + offset, span.data, span.length);
        offset += span.length;
    }
    wchar_t *wide = utf8_to_wide(utf8, utf8_length);
    HeapFree(GetProcessHeap(), 0, utf8);
    return wide;
}

extern "C" __declspec(dllexport) int32_t undertow_main(const undertow_native_api_v1 *api,
                                                         const void *args, size_t args_len)
{
    undertow_native_span data = { 0 };
    wchar_t *script = NULL;
    uint32_t count;
    int32_t result = 1;

    if (!api || api->size < sizeof(*api) || api->version != UNDERTOW_NATIVE_ABI_VERSION ||
        !api->write || !api->write_error || !api->cancelled || !args || args_len < 8)
        return 2;
    shellpower_set_api(api);
    count = undertow_native_le32((const uint8_t *)args);
    if (count > 256 || undertow_native_data(args, args_len, &data) != 0) {
        shellpower_errorf(L"shellpower: invalid native module arguments\n");
        result = 2;
        goto exit;
    }
    if (count > 0 && data.length > 0) {
        shellpower_errorf(L"shellpower: provide an inline command or a script file, not both\n");
        result = 2;
        goto exit;
    }
    if (count == 0 && data.length == 0) {
        shellpower_errorf(L"shellpower: a PowerShell command or script file is required\n");
        result = 2;
        goto exit;
    }
    if (api->cancelled(api->context) != 0) {
        result = 130;
        goto exit;
    }

    if (data.length > 0 && data.length >= 2 && data.data[0] == 0xff && data.data[1] == 0xfe)
        script = utf16le_to_wide(data.data, data.length);
    else if (data.length > 0)
        script = utf8_to_wide(data.data, data.length);
    else
        script = script_from_args(args, args_len, count);
    if (!script) {
        shellpower_errorf(L"shellpower: script must be valid UTF-8 or BOM-marked UTF-16LE without NUL characters\n");
        result = 2;
        goto exit;
    }

    result = shExecScript(script) ? 0 : 1;

exit:
    if (script) HeapFree(GetProcessHeap(), 0, script);
    shellpower_set_api(NULL);
    return result;
}

extern "C" __declspec(dllexport) int32_t undertow_shell_main(const undertow_native_shell_api_v1 *api)
{
    mscorlib::_AppDomain *app_domain = NULL;
    RT_CONTEXT runtime = { 0 };
    VARIANT powershell = { 0 };
    char *line = NULL;
    wchar_t *script = NULL;
    int32_t result = 1;

    if (!api || api->size < sizeof(*api) || api->version != UNDERTOW_NATIVE_SHELL_ABI_VERSION ||
        !api->write || !api->write_error || !api->read || !api->cancelled)
        return 2;
    shellpower_set_api((const undertow_native_api_v1 *)api);
    if (!clr::clrInitRuntime(&runtime, &app_domain)) goto exit;
    if (!shCreate(app_domain, &powershell)) goto exit;
    shPatchAll(app_domain);
    shell_write(api, "Windows ShellPower for Undertow\r\nType exit to close the session.\r\n\r\n");

    while (api->cancelled(api->context) == 0 && shell_read_line(api, &line)) {
        if (shell_is_exit(line)) {
            result = 0;
            goto exit;
        }
        script = utf8_to_wide((const uint8_t *)line, strlen(line));
        HeapFree(GetProcessHeap(), 0, line);
        line = NULL;
        if (!script) {
            shellpower_errorf(L"shellpower: command must be valid UTF-8\r\n");
            continue;
        }
        shell_invoke(api, app_domain, powershell, script);
        HeapFree(GetProcessHeap(), 0, script);
        script = NULL;
    }
    result = 0;

exit:
    if (line) HeapFree(GetProcessHeap(), 0, line);
    if (script) HeapFree(GetProcessHeap(), 0, script);
    if (app_domain && powershell.punkVal) shDispose(app_domain, powershell);
    VariantClear(&powershell);
    clr::clrDestroyRuntime(&runtime, app_domain);
    shellpower_set_api(NULL);
    return result;
}
