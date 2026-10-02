#include "undertow_native.h"
#include <windows.h>
#include <string.h>

int32_t undertow_main(const undertow_native_api_v1 *api, const void *args, size_t args_len) {
    const uint8_t *p = (const uint8_t *)args;
    uint32_t count, i;
    if (!api || api->version != UNDERTOW_NATIVE_ABI_VERSION || api->size < sizeof(*api) || !p || args_len < 8) return 2;
    count = undertow_native_le32(p);
    if (count > 256) return 2;
    if (undertow_native_printf(api, "Hello from a native module; argc=%u\n", count) != 0) return 3;
    for (i = 0; i < count; ++i) {
        undertow_native_span arg;
        if (undertow_native_arg(args, args_len, i, &arg) != 0) return 2;
        if (undertow_native_printf(api, "arg[%u]=", i) != 0) return 3;
        if (api->write(api->context, arg.data, arg.length) != 0) return 3;
        if (api->write(api->context, "\n", 1) != 0) return 3;
        if (api->cancelled(api->context)) return 130;
        if (arg.length == 6 && memcmp(arg.data, "--fail", 6) == 0) return 7;
        if (arg.length == 6 && memcmp(arg.data, "--wait", 6) == 0) {
            while (!api->cancelled(api->context)) Sleep(10);
            return 130;
        }
    }
    {
        undertow_native_span data;
        if (undertow_native_data(args, args_len, &data) != 0) return 2;
        if (undertow_native_printf(api, "opaque data bytes=%u\n", data.length) != 0) return 3;
    }
    return 0;
}
