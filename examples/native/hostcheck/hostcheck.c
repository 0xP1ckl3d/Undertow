#define WIN32_LEAN_AND_MEAN
#define _WIN32_WINNT 0x0600

#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <iphlpapi.h>
#include <sddl.h>
#include <stdio.h>
#include <stdint.h>

#include "undertow_native.h"

static int write_wide(
    const undertow_native_api_v1 *api,
    const WCHAR *value)
{
    int needed;
    char *utf8;

    if (!value) {
        return api->write(api->context, "(null)", 6);
    }

    needed = WideCharToMultiByte(
        CP_UTF8,
        0,
        value,
        -1,
        NULL,
        0,
        NULL,
        NULL);

    if (needed <= 1) {
        return -1;
    }

    utf8 = (char *)HeapAlloc(
        GetProcessHeap(),
        0,
        (SIZE_T)needed);

    if (!utf8) {
        return -1;
    }

    if (!WideCharToMultiByte(
            CP_UTF8,
            0,
            value,
            -1,
            utf8,
            needed,
            NULL,
            NULL)) {
        HeapFree(GetProcessHeap(), 0, utf8);
        return -1;
    }

    /* Exclude trailing NUL. */
    if (api->write(
            api->context,
            utf8,
            (size_t)(needed - 1)) != 0) {
        HeapFree(GetProcessHeap(), 0, utf8);
        return -1;
    }

    HeapFree(GetProcessHeap(), 0, utf8);
    return 0;
}

static const char *integrity_name(DWORD rid)
{
    if (rid < SECURITY_MANDATORY_MEDIUM_RID)
        return "Low";

    if (rid < SECURITY_MANDATORY_HIGH_RID)
        return "Medium";

    if (rid < SECURITY_MANDATORY_SYSTEM_RID)
        return "High";

    if (rid < SECURITY_MANDATORY_PROTECTED_PROCESS_RID)
        return "System";

    return "Protected";
}

static int print_system_info(
    const undertow_native_api_v1 *api)
{
    WCHAR computer[256];
    DWORD computer_len = ARRAYSIZE(computer);

    WCHAR user[256];
    DWORD user_len = ARRAYSIZE(user);

    WCHAR image[MAX_PATH * 4];
    DWORD image_len = ARRAYSIZE(image);

    SYSTEM_INFO si;
    MEMORYSTATUSEX memory;

    computer[0] = L'\0';
    user[0] = L'\0';
    image[0] = L'\0';

    GetComputerNameExW(
        ComputerNameDnsFullyQualified,
        computer,
        &computer_len);

    GetUserNameW(
        user,
        &user_len);

    QueryFullProcessImageNameW(
        GetCurrentProcess(),
        0,
        image,
        &image_len);

    GetNativeSystemInfo(&si);

    ZeroMemory(&memory, sizeof(memory));
    memory.dwLength = sizeof(memory);
    GlobalMemoryStatusEx(&memory);

    undertow_native_printf(
        api,
        "=== HOST ===\n");

    api->write(api->context, "Computer       : ", 17);
    write_wide(api, computer);
    api->write(api->context, "\n", 1);

    api->write(api->context, "User           : ", 17);
    write_wide(api, user);
    api->write(api->context, "\n", 1);

    api->write(api->context, "Process        : ", 17);
    write_wide(api, image);
    api->write(api->context, "\n", 1);

    undertow_native_printf(
        api,
        "PID            : %lu\n",
        (unsigned long)GetCurrentProcessId());

    undertow_native_printf(
        api,
        "Undertow PID   : %lu\n",
        (unsigned long)api->process_id);

    undertow_native_printf(
        api,
        "Processors     : %lu\n",
        (unsigned long)si.dwNumberOfProcessors);

    undertow_native_printf(
        api,
        "Page size      : %lu\n",
        (unsigned long)si.dwPageSize);

    undertow_native_printf(
        api,
        "Physical RAM   : %llu MB\n",
        (unsigned long long)(
            memory.ullTotalPhys / (1024ULL * 1024ULL)));

    undertow_native_printf(
        api,
        "System uptime  : %llu seconds\n",
        (unsigned long long)(
            GetTickCount64() / 1000ULL));

    return 0;
}

static int print_token_info(
    const undertow_native_api_v1 *api)
{
    HANDLE token = NULL;
    TOKEN_ELEVATION elevation;
    DWORD returned = 0;

    DWORD needed = 0;
    TOKEN_MANDATORY_LABEL *integrity = NULL;

    TOKEN_USER *token_user = NULL;
    LPWSTR sid_string = NULL;

    if (!OpenProcessToken(
            GetCurrentProcess(),
            TOKEN_QUERY,
            &token)) {
        undertow_native_printf(
            api,
            "\nOpenProcessToken failed: %lu\n",
            (unsigned long)GetLastError());
        return 1;
    }

    undertow_native_printf(
        api,
        "\n=== TOKEN ===\n");

    ZeroMemory(&elevation, sizeof(elevation));

    if (GetTokenInformation(
            token,
            TokenElevation,
            &elevation,
            sizeof(elevation),
            &returned)) {
        undertow_native_printf(
            api,
            "Elevated       : %s\n",
            elevation.TokenIsElevated ? "Yes" : "No");
    }

    /*
     * Token user.
     */
    GetTokenInformation(
        token,
        TokenUser,
        NULL,
        0,
        &needed);

    if (needed) {
        token_user = (TOKEN_USER *)HeapAlloc(
            GetProcessHeap(),
            HEAP_ZERO_MEMORY,
            needed);

        if (token_user &&
            GetTokenInformation(
                token,
                TokenUser,
                token_user,
                needed,
                &needed) &&
            ConvertSidToStringSidW(
                token_user->User.Sid,
                &sid_string)) {

            api->write(
                api->context,
                "Token SID      : ",
                17);

            write_wide(api, sid_string);

            api->write(
                api->context,
                "\n",
                1);

            LocalFree(sid_string);
            sid_string = NULL;
        }
    }

    /*
     * Integrity level.
     */
    needed = 0;

    GetTokenInformation(
        token,
        TokenIntegrityLevel,
        NULL,
        0,
        &needed);

    if (needed) {
        integrity =
            (TOKEN_MANDATORY_LABEL *)HeapAlloc(
                GetProcessHeap(),
                HEAP_ZERO_MEMORY,
                needed);

        if (integrity &&
            GetTokenInformation(
                token,
                TokenIntegrityLevel,
                integrity,
                needed,
                &needed)) {

            PSID sid =
                integrity->Label.Sid;

            DWORD sub_count =
                *GetSidSubAuthorityCount(sid);

            DWORD rid =
                *GetSidSubAuthority(
                    sid,
                    sub_count - 1);

            undertow_native_printf(
                api,
                "Integrity      : %s (0x%lx)\n",
                integrity_name(rid),
                (unsigned long)rid);
        }
    }

    if (integrity)
        HeapFree(
            GetProcessHeap(),
            0,
            integrity);

    if (token_user)
        HeapFree(
            GetProcessHeap(),
            0,
            token_user);

    CloseHandle(token);
    return 0;
}

static int print_privileges(
    const undertow_native_api_v1 *api)
{
    HANDLE token = NULL;
    DWORD needed = 0;
    TOKEN_PRIVILEGES *privs = NULL;
    DWORD i;

    if (!OpenProcessToken(
            GetCurrentProcess(),
            TOKEN_QUERY,
            &token)) {
        return 1;
    }

    GetTokenInformation(
        token,
        TokenPrivileges,
        NULL,
        0,
        &needed);

    if (!needed) {
        CloseHandle(token);
        return 1;
    }

    privs = (TOKEN_PRIVILEGES *)HeapAlloc(
        GetProcessHeap(),
        HEAP_ZERO_MEMORY,
        needed);

    if (!privs) {
        CloseHandle(token);
        return 1;
    }

    if (!GetTokenInformation(
            token,
            TokenPrivileges,
            privs,
            needed,
            &needed)) {
        HeapFree(
            GetProcessHeap(),
            0,
            privs);

        CloseHandle(token);
        return 1;
    }

    undertow_native_printf(
        api,
        "\n=== TOKEN PRIVILEGES (%lu) ===\n",
        (unsigned long)privs->PrivilegeCount);

    for (i = 0;
         i < privs->PrivilegeCount;
         ++i) {

        WCHAR name[256];
        DWORD name_len =
            ARRAYSIZE(name);

        DWORD attr =
            privs->Privileges[i].Attributes;

        if (api->cancelled(
                api->context)) {
            HeapFree(
                GetProcessHeap(),
                0,
                privs);

            CloseHandle(token);
            return 130;
        }

        if (!LookupPrivilegeNameW(
                NULL,
                &privs->Privileges[i].Luid,
                name,
                &name_len)) {
            continue;
        }

        api->write(
            api->context,
            "  ",
            2);

        write_wide(
            api,
            name);

        undertow_native_printf(
            api,
            " [%s%s%s]\n",
            (attr & SE_PRIVILEGE_ENABLED)
                ? "enabled"
                : "disabled",
            (attr & SE_PRIVILEGE_ENABLED_BY_DEFAULT)
                ? ", default"
                : "",
            (attr & SE_PRIVILEGE_USED_FOR_ACCESS)
                ? ", used"
                : "");
    }

    HeapFree(
        GetProcessHeap(),
        0,
        privs);

    CloseHandle(token);
    return 0;
}

static const char *adapter_status(
    IF_OPER_STATUS status)
{
    switch (status) {
    case IfOperStatusUp:
        return "up";
    case IfOperStatusDown:
        return "down";
    case IfOperStatusDormant:
        return "dormant";
    case IfOperStatusNotPresent:
        return "not-present";
    case IfOperStatusLowerLayerDown:
        return "lower-layer-down";
    case IfOperStatusTesting:
        return "testing";
    default:
        return "unknown";
    }
}

static int print_network(
    const undertow_native_api_v1 *api)
{
    ULONG size = 16 * 1024;
    ULONG result;
    IP_ADAPTER_ADDRESSES *addresses;
    IP_ADAPTER_ADDRESSES *adapter;

    addresses =
        (IP_ADAPTER_ADDRESSES *)HeapAlloc(
            GetProcessHeap(),
            HEAP_ZERO_MEMORY,
            size);

    if (!addresses)
        return 1;

    result =
        GetAdaptersAddresses(
            AF_UNSPEC,
            GAA_FLAG_SKIP_ANYCAST |
            GAA_FLAG_SKIP_MULTICAST |
            GAA_FLAG_SKIP_DNS_SERVER,
            NULL,
            addresses,
            &size);

    if (result ==
        ERROR_BUFFER_OVERFLOW) {

        HeapFree(
            GetProcessHeap(),
            0,
            addresses);

        addresses =
            (IP_ADAPTER_ADDRESSES *)HeapAlloc(
                GetProcessHeap(),
                HEAP_ZERO_MEMORY,
                size);

        if (!addresses)
            return 1;

        result =
            GetAdaptersAddresses(
                AF_UNSPEC,
                GAA_FLAG_SKIP_ANYCAST |
                GAA_FLAG_SKIP_MULTICAST |
                GAA_FLAG_SKIP_DNS_SERVER,
                NULL,
                addresses,
                &size);
    }

    if (result != NO_ERROR) {
        undertow_native_printf(
            api,
            "\nGetAdaptersAddresses failed: %lu\n",
            (unsigned long)result);

        HeapFree(
            GetProcessHeap(),
            0,
            addresses);

        return 1;
    }

    undertow_native_printf(
        api,
        "\n=== NETWORK ADAPTERS ===\n");

    for (adapter = addresses;
         adapter;
         adapter = adapter->Next) {

        ULONG i;

        if (api->cancelled(
                api->context)) {
            HeapFree(
                GetProcessHeap(),
                0,
                addresses);

            return 130;
        }

        if (adapter->OperStatus !=
            IfOperStatusUp)
            continue;

        api->write(
            api->context,
            "\n",
            1);

        if (adapter->FriendlyName)
            write_wide(
                api,
                adapter->FriendlyName);

        undertow_native_printf(
            api,
            "\n  ifIndex : %lu\n"
            "  status  : %s\n"
            "  MTU     : %lu\n"
            "  MAC     : ",
            (unsigned long)adapter->IfIndex,
            adapter_status(
                adapter->OperStatus),
            (unsigned long)adapter->Mtu);

        if (adapter->PhysicalAddressLength) {
            for (i = 0;
                 i < adapter->PhysicalAddressLength;
                 ++i) {

                undertow_native_printf(
                    api,
                    "%s%02X",
                    i ? ":" : "",
                    adapter->PhysicalAddress[i]);
            }
        } else {
            api->write(
                api->context,
                "(none)",
                6);
        }

        api->write(
            api->context,
            "\n",
            1);
    }

    HeapFree(
        GetProcessHeap(),
        0,
        addresses);

    return 0;
}

int32_t undertow_main(
    const undertow_native_api_v1 *api,
    const void *args,
    size_t args_len)
{
    (void)args;
    (void)args_len;

    if (!api ||
        api->version !=
            UNDERTOW_NATIVE_ABI_VERSION ||
        api->size <
            sizeof(*api)) {
        return 2;
    }

    undertow_native_printf(
        api,
        "Undertow native hostcheck\n"
        "=========================\n");

    if (print_system_info(api) != 0)
        return 3;

    if (api->cancelled(api->context))
        return 130;

    if (print_token_info(api) != 0)
        return 4;

    if (api->cancelled(api->context))
        return 130;

    if (print_privileges(api) != 0)
        return 5;

    if (api->cancelled(api->context))
        return 130;

    if (print_network(api) != 0)
        return 6;

    return api->cancelled(
        api->context)
        ? 130
        : 0;
}