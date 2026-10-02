#define WIN32_LEAN_AND_MEAN
#define SECURITY_WIN32

#include <windows.h>
#include <security.h>
#include <wincred.h>
#include <objbase.h>
#include <stdint.h>
#include <limits.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>
#include <wchar.h>

#include "undertow_native.h"

#define ASKPASS_TEXT_BUFFER_CHARS 1024u

static int askpass_errorf(const undertow_native_api_v1 *api, const char *format, ...) {
    char buffer[1024];
    int n;
    va_list arguments;

    va_start(arguments, format);
    n = vsnprintf(buffer, sizeof(buffer), format, arguments);
    va_end(arguments);

    if (n < 0) return -1;
    if ((size_t)n >= sizeof(buffer)) n = (int)(sizeof(buffer) - 1);
    return api->write_error(api->context, buffer, (size_t)n);
}

static DWORD askpass_arg_to_wide(undertow_native_span span, WCHAR **out) {
    int chars;
    WCHAR *buffer;

    if (!out) return ERROR_INVALID_PARAMETER;
    *out = NULL;

    if (span.length == 0) {
        buffer = (WCHAR *)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, sizeof(WCHAR));
        if (!buffer) return ERROR_NOT_ENOUGH_MEMORY;
        *out = buffer;
        return ERROR_SUCCESS;
    }

    if (!span.data || span.length > INT_MAX) return ERROR_INVALID_PARAMETER;
    if (memchr(span.data, '\0', span.length) != NULL) return ERROR_INVALID_DATA;

    chars = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS,
                                (LPCCH)span.data, (int)span.length,
                                NULL, 0);
    if (chars <= 0) return GetLastError();

    buffer = (WCHAR *)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY,
                                ((SIZE_T)chars + 1u) * sizeof(WCHAR));
    if (!buffer) return ERROR_NOT_ENOUGH_MEMORY;

    if (MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS,
                            (LPCCH)span.data, (int)span.length,
                            buffer, chars) != chars) {
        DWORD error = GetLastError();
        HeapFree(GetProcessHeap(), 0, buffer);
        return error ? error : ERROR_NO_UNICODE_TRANSLATION;
    }

    buffer[chars] = L'\0';
    *out = buffer;
    return ERROR_SUCCESS;
}

static DWORD askpass_wide_to_utf8(const WCHAR *text, char **out, SIZE_T *allocated) {
    int bytes;
    char *buffer;

    if (!text || !out || !allocated) return ERROR_INVALID_PARAMETER;
    *out = NULL;
    *allocated = 0;

    bytes = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS,
                                text, -1, NULL, 0, NULL, NULL);
    if (bytes <= 0) return GetLastError();

    buffer = (char *)HeapAlloc(GetProcessHeap(), 0, (SIZE_T)bytes);
    if (!buffer) return ERROR_NOT_ENOUGH_MEMORY;

    if (WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS,
                            text, -1, buffer, bytes, NULL, NULL) != bytes) {
        DWORD error = GetLastError();
        HeapFree(GetProcessHeap(), 0, buffer);
        return error ? error : ERROR_NO_UNICODE_TRANSLATION;
    }

    *out = buffer;
    *allocated = (SIZE_T)bytes;
    return ERROR_SUCCESS;
}

static BOOL askpass_current_user(WCHAR *buffer, DWORD cch_buffer) {
    DWORD length;

    if (!buffer || cch_buffer == 0) return FALSE;
    buffer[0] = L'\0';

    length = cch_buffer;
    if (GetUserNameExW(NameSamCompatible, buffer, &length) && buffer[0] != L'\0') {
        return TRUE;
    }

    length = cch_buffer;
    if (GetUserNameW(buffer, &length) && buffer[0] != L'\0') {
        return TRUE;
    }

    return FALSE;
}

static void askpass_normalize_logon_name(const WCHAR *username, const WCHAR *domain,
                                          WCHAR *out_username, size_t out_username_chars,
                                          WCHAR *out_domain, size_t out_domain_chars) {
    const WCHAR *slash;
    size_t domain_len;

    if (!out_username || !out_domain || out_username_chars == 0 || out_domain_chars == 0) return;
    out_username[0] = L'\0';
    out_domain[0] = L'\0';

    if (!username) return;

    if (domain && domain[0] != L'\0') {
        wcsncpy_s(out_domain, out_domain_chars, domain, _TRUNCATE);
        wcsncpy_s(out_username, out_username_chars, username, _TRUNCATE);
        return;
    }

    slash = wcschr(username, L'\\');
    if (slash && slash != username && slash[1] != L'\0') {
        domain_len = (size_t)(slash - username);
        if (domain_len >= out_domain_chars) domain_len = out_domain_chars - 1;
        wmemcpy(out_domain, username, domain_len);
        out_domain[domain_len] = L'\0';
        wcsncpy_s(out_username, out_username_chars, slash + 1, _TRUNCATE);
        return;
    }

    /* UPNs (user@domain) and bare local names are valid with lpDomain == NULL. */
    wcsncpy_s(out_username, out_username_chars, username, _TRUNCATE);
}

int32_t undertow_main(const undertow_native_api_v1 *api,
                      const void *args, size_t args_len) {
    const uint8_t *encoded = (const uint8_t *)args;
    undertow_native_span caption_arg;
    undertow_native_span message_arg;
    undertow_native_span opaque_data;
    WCHAR *caption = NULL;
    WCHAR *message = NULL;
    WCHAR current_user[512];
    WCHAR empty_password[1] = { L'\0' };
    BYTE *input_auth = NULL;
    DWORD input_auth_size = 0;
    LPVOID output_auth = NULL;
    ULONG output_auth_size = 0;
    ULONG auth_package = 0;
    BOOL save = FALSE;
    CREDUI_INFOW ui;
    WCHAR username[ASKPASS_TEXT_BUFFER_CHARS];
    WCHAR domain[ASKPASS_TEXT_BUFFER_CHARS];
    WCHAR password[ASKPASS_TEXT_BUFFER_CHARS];
    WCHAR logon_username[ASKPASS_TEXT_BUFFER_CHARS];
    WCHAR logon_domain[ASKPASS_TEXT_BUFFER_CHARS];
    DWORD username_chars = ASKPASS_TEXT_BUFFER_CHARS;
    DWORD domain_chars = ASKPASS_TEXT_BUFFER_CHARS;
    DWORD password_chars = ASKPASS_TEXT_BUFFER_CHARS;
    HANDLE token = NULL;
    BOOL valid = FALSE;
    char *username_utf8 = NULL;
    char *domain_utf8 = NULL;
    char *password_utf8 = NULL;
    SIZE_T username_utf8_size = 0;
    SIZE_T domain_utf8_size = 0;
    SIZE_T password_utf8_size = 0;
    DWORD result;
    DWORD error;
    DWORD logon_error = ERROR_SUCCESS;
    uint32_t argc;
    int32_t status = 0;

    if (!api || api->version != UNDERTOW_NATIVE_ABI_VERSION || api->size < sizeof(*api)) {
        return 2;
    }

    if (!encoded || args_len < 8) {
        askpass_errorf(api, "Usage: run-native askpass.module \"Caption\" \"Message\"\n");
        return 2;
    }

    argc = undertow_native_le32(encoded);
    if (argc != 2 ||
        undertow_native_arg(args, args_len, 0, &caption_arg) != 0 ||
        undertow_native_arg(args, args_len, 1, &message_arg) != 0 ||
        undertow_native_data(args, args_len, &opaque_data) != 0) {
        askpass_errorf(api, "Usage: run-native askpass.module \"Caption\" \"Message\"\n");
        return 2;
    }
    (void)opaque_data;

    if (api->cancelled(api->context)) return 130;

    error = askpass_arg_to_wide(caption_arg, &caption);
    if (error != ERROR_SUCCESS) {
        askpass_errorf(api, "askpass: invalid UTF-8 caption (error %lu)\n", (unsigned long)error);
        status = 3;
        goto cleanup;
    }

    error = askpass_arg_to_wide(message_arg, &message);
    if (error != ERROR_SUCCESS) {
        askpass_errorf(api, "askpass: invalid UTF-8 message (error %lu)\n", (unsigned long)error);
        status = 3;
        goto cleanup;
    }

    if (!askpass_current_user(current_user, (DWORD)(sizeof(current_user) / sizeof(current_user[0])))) {
        error = GetLastError();
        askpass_errorf(api, "askpass: could not determine current user (error %lu)\n",
                       (unsigned long)error);
        status = 4;
        goto cleanup;
    }

    SetLastError(ERROR_SUCCESS);
    if (!CredPackAuthenticationBufferW(CRED_PACK_GENERIC_CREDENTIALS,
                                       current_user,
                                       empty_password,
                                       NULL,
                                       &input_auth_size)) {
        error = GetLastError();
        if (error != ERROR_INSUFFICIENT_BUFFER || input_auth_size == 0) {
            askpass_errorf(api, "askpass: CredPackAuthenticationBufferW size query failed (error %lu)\n",
                           (unsigned long)error);
            status = 5;
            goto cleanup;
        }
    }

    if (input_auth_size == 0) {
        askpass_errorf(api, "askpass: CredPackAuthenticationBufferW returned an empty input buffer\n");
        status = 5;
        goto cleanup;
    }

    input_auth = (BYTE *)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, input_auth_size);
    if (!input_auth) {
        status = 6;
        goto cleanup;
    }

    if (!CredPackAuthenticationBufferW(CRED_PACK_GENERIC_CREDENTIALS,
                                       current_user,
                                       empty_password,
                                       input_auth,
                                       &input_auth_size)) {
        error = GetLastError();
        askpass_errorf(api, "askpass: CredPackAuthenticationBufferW failed (error %lu)\n",
                       (unsigned long)error);
        status = 5;
        goto cleanup;
    }

    ZeroMemory(&ui, sizeof(ui));
    ui.cbSize = sizeof(ui);
    ui.hwndParent = NULL;
    ui.pszCaptionText = caption;
    ui.pszMessageText = message;
    ui.hbmBanner = NULL;

    result = CredUIPromptForWindowsCredentialsW(
        &ui,
        0,
        &auth_package,
        input_auth,
        input_auth_size,
        &output_auth,
        &output_auth_size,
        &save,
        CREDUIWIN_GENERIC | CREDUIWIN_ENUMERATE_CURRENT_USER);

    if (result == ERROR_CANCELLED) {
        api->write(api->context, "Credential prompt cancelled.\n", 29);
        status = 0;
        goto cleanup;
    }

    if (result != ERROR_SUCCESS) {
        askpass_errorf(api, "askpass: CredUIPromptForWindowsCredentialsW failed (error %lu)\n",
                       (unsigned long)result);
        status = 7;
        goto cleanup;
    }

    ZeroMemory(username, sizeof(username));
    ZeroMemory(domain, sizeof(domain));
    ZeroMemory(password, sizeof(password));

    if (!CredUnPackAuthenticationBufferW(
            0,
            output_auth,
            output_auth_size,
            username,
            &username_chars,
            domain,
            &domain_chars,
            password,
            &password_chars)) {
        error = GetLastError();
        askpass_errorf(api, "askpass: CredUnPackAuthenticationBufferW failed (error %lu)\n",
                       (unsigned long)error);
        status = 8;
        goto cleanup;
    }

    if (output_auth) {
        SecureZeroMemory(output_auth, output_auth_size);
        CoTaskMemFree(output_auth);
        output_auth = NULL;
        output_auth_size = 0;
    }

    ZeroMemory(logon_username, sizeof(logon_username));
    ZeroMemory(logon_domain, sizeof(logon_domain));
    askpass_normalize_logon_name(username, domain,
                                 logon_username, ASKPASS_TEXT_BUFFER_CHARS,
                                 logon_domain, ASKPASS_TEXT_BUFFER_CHARS);

    valid = LogonUserW(logon_username,
                       logon_domain[0] != L'\0' ? logon_domain : NULL,
                       password,
                       LOGON32_LOGON_INTERACTIVE,
                       LOGON32_PROVIDER_DEFAULT,
                       &token);
    if (!valid) logon_error = GetLastError();
    if (token) {
        CloseHandle(token);
        token = NULL;
    }

    error = askpass_wide_to_utf8(logon_username, &username_utf8, &username_utf8_size);
    if (error != ERROR_SUCCESS) {
        askpass_errorf(api, "askpass: username UTF-8 conversion failed (error %lu)\n",
                       (unsigned long)error);
        status = 9;
        goto cleanup;
    }

    error = askpass_wide_to_utf8(logon_domain, &domain_utf8, &domain_utf8_size);
    if (error != ERROR_SUCCESS) {
        askpass_errorf(api, "askpass: domain UTF-8 conversion failed (error %lu)\n",
                       (unsigned long)error);
        status = 9;
        goto cleanup;
    }

    error = askpass_wide_to_utf8(password, &password_utf8, &password_utf8_size);
    if (error != ERROR_SUCCESS) {
        askpass_errorf(api, "askpass: password UTF-8 conversion failed (error %lu)\n",
                       (unsigned long)error);
        status = 9;
        goto cleanup;
    }

    if (valid) {
        if (undertow_native_printf(api,
                                   "Valid Credential\n\tDomain: %s\n\tUsername: %s\n\tPassword: %s\n",
                                   domain_utf8, username_utf8, password_utf8) != 0) {
            status = 10;
            goto cleanup;
        }
    } else {
        if (undertow_native_printf(api,
                                   "Invalid Credential\n\tDomain: %s\n\tUsername: %s\n\tPassword: %s\n\tLogonUserW error: %lu\n",
                                   domain_utf8, username_utf8, password_utf8,
                                   (unsigned long)logon_error) != 0) {
            status = 10;
            goto cleanup;
        }
    }

    status = api->cancelled(api->context) ? 130 : 0;

cleanup:
    if (token) CloseHandle(token);

    if (output_auth) {
        SecureZeroMemory(output_auth, output_auth_size);
        CoTaskMemFree(output_auth);
    }

    if (input_auth) {
        SecureZeroMemory(input_auth, input_auth_size);
        HeapFree(GetProcessHeap(), 0, input_auth);
    }

    if (password_utf8) {
        SecureZeroMemory(password_utf8, password_utf8_size);
        HeapFree(GetProcessHeap(), 0, password_utf8);
    }
    if (domain_utf8) HeapFree(GetProcessHeap(), 0, domain_utf8);
    if (username_utf8) HeapFree(GetProcessHeap(), 0, username_utf8);

    SecureZeroMemory(password, sizeof(password));
    SecureZeroMemory(empty_password, sizeof(empty_password));

    if (message) HeapFree(GetProcessHeap(), 0, message);
    if (caption) HeapFree(GetProcessHeap(), 0, caption);

    return status;
}
