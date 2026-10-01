#ifndef UNDERTOW_NATIVE_H
#define UNDERTOW_NATIVE_H

#include <stddef.h>
#include <stdint.h>
#include <stdarg.h>
#include <stdio.h>

#ifdef __cplusplus
extern "C" {
#endif

#define UNDERTOW_NATIVE_ABI_VERSION 1u
#define UNDERTOW_NATIVE_OS_WINDOWS 1u
#define UNDERTOW_NATIVE_ARCH_AMD64 1u

typedef int32_t (*undertow_native_write_fn)(uintptr_t context, const void *bytes, size_t length);
typedef int32_t (*undertow_native_state_fn)(uintptr_t context);

typedef struct undertow_native_api_v1 {
    uint32_t size;
    uint32_t version;
    uintptr_t context;
    undertow_native_write_fn write;
    undertow_native_write_fn write_error;
    undertow_native_state_fn cancelled;
    undertow_native_state_fn job_state; /* 0 running, 1 cancellation requested */
    uint32_t os;
    uint32_t arch;
    uint32_t process_id;
    uint32_t reserved;
} undertow_native_api_v1;

typedef struct undertow_native_span {
    const uint8_t *data;
    uint32_t length;
} undertow_native_span;

/* Arguments: LE32 argc, then [LE32 length, UTF-8 bytes] per argument,
   then LE32 opaque-data length and its binary bytes. All spans are borrowed. */
static inline uint32_t undertow_native_le32(const uint8_t *p) {
    return (uint32_t)p[0] | (uint32_t)p[1] << 8 | (uint32_t)p[2] << 16 | (uint32_t)p[3] << 24;
}

static inline int undertow_native_arg(const void *buffer, size_t length, uint32_t index, undertow_native_span *out) {
    const uint8_t *p = (const uint8_t *)buffer;
    size_t offset = 4;
    uint32_t i, count;
    if (!p || !out || length < 8) return -1;
    count = undertow_native_le32(p);
    if (count > 256 || index >= count) return -1;
    for (i = 0; i < count; ++i) {
        uint32_t n;
        if (offset > length || length - offset < 4) return -1;
        n = undertow_native_le32(p + offset); offset += 4;
        if (n > length - offset) return -1;
        if (i == index) { out->data = p + offset; out->length = n; return 0; }
        offset += n;
    }
    return -1;
}

static inline int undertow_native_data(const void *buffer, size_t length, undertow_native_span *out) {
    const uint8_t *p = (const uint8_t *)buffer;
    size_t offset = 4;
    uint32_t i, count, n;
    if (!p || !out || length < 8) return -1;
    count = undertow_native_le32(p);
    if (count > 256) return -1;
    for (i = 0; i < count; ++i) {
        if (offset > length || length - offset < 4) return -1;
        n = undertow_native_le32(p + offset); offset += 4;
        if (n > length - offset) return -1;
        offset += n;
    }
    if (offset > length || length - offset < 4) return -1;
    n = undertow_native_le32(p + offset); offset += 4;
    if (n != length - offset) return -1;
    out->data = p + offset; out->length = n;
    return 0;
}

static inline int undertow_native_printf(const undertow_native_api_v1 *api, const char *format, ...) {
    char buffer[8192];
    int n;
    va_list arguments;
    va_start(arguments, format);
    n = vsnprintf(buffer, sizeof(buffer), format, arguments);
    va_end(arguments);
    if (n < 0 || (size_t)n >= sizeof(buffer)) return -1;
    return api->write(api->context, buffer, (size_t)n);
}

/* Export this exact undecorated name from a Windows x64 DLL. */
__declspec(dllexport) int32_t undertow_main(const undertow_native_api_v1 *api,
                                             const void *args, size_t args_len);

#ifdef __cplusplus
}
#endif
#endif
