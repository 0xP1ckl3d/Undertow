#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <stdint.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>

typedef struct { char *original; char *buffer; int length; int size; } datap;
typedef struct { char *original; char *buffer; int length; int size; } formatp;
typedef int (__cdecl *undertow_output_fn)(uintptr_t, int, const char *, size_t);
static __declspec(thread) undertow_output_fn active_output;
static __declspec(thread) uintptr_t active_context;

__declspec(dllexport) void BofEnter(undertow_output_fn output, uintptr_t context) {
    active_output = output;
    active_context = context;
}
__declspec(dllexport) void BofLeave(void) {
    active_output = NULL;
    active_context = 0;
}
static void emit(int type, const char *data, size_t length) {
    if (active_output && data && length <= 4 * 1024 * 1024) active_output(active_context, type, data, length);
}
__declspec(dllexport) void BeaconOutput(int type, const char *data, int length) {
    if (length > 0) emit(type, data, (size_t)length);
}
__declspec(dllexport) void BeaconPrintf(int type, const char *format, ...) {
    va_list args;
    int needed;
    char *buffer;
    if (!format) return;
    va_start(args, format);
    needed = _vscprintf(format, args);
    va_end(args);
    if (needed < 0 || needed > 1024 * 1024) return;
    buffer = (char *)HeapAlloc(GetProcessHeap(), 0, (size_t)needed + 1);
    if (!buffer) return;
    va_start(args, format);
    vsnprintf(buffer, (size_t)needed + 1, format, args);
    va_end(args);
    emit(type, buffer, (size_t)needed);
    HeapFree(GetProcessHeap(), 0, buffer);
}
static uint16_t be16(const unsigned char *p) { return (uint16_t)((uint16_t)p[0] << 8 | p[1]); }
static uint32_t be32(const unsigned char *p) { return (uint32_t)p[0] << 24 | (uint32_t)p[1] << 16 | (uint32_t)p[2] << 8 | p[3]; }
static void put32(unsigned char *p, uint32_t n) { p[0]=(unsigned char)(n>>24); p[1]=(unsigned char)(n>>16); p[2]=(unsigned char)(n>>8); p[3]=(unsigned char)n; }

__declspec(dllexport) void BeaconDataParse(datap *parser, char *buffer, int size) {
    uint32_t declared;
    if (!parser) return;
    memset(parser, 0, sizeof(*parser));
    if (!buffer || size < 4) return;
    declared = be32((const unsigned char *)buffer);
    if (declared > (uint32_t)(size - 4)) return;
    parser->original = buffer + 4; parser->buffer = buffer + 4;
    parser->length = (int)declared; parser->size = (int)declared;
}
__declspec(dllexport) char *BeaconDataPtr(datap *parser, int size) {
    char *value;
    if (!parser || !parser->buffer || size < 0 || size > parser->length) return NULL;
    value = parser->buffer;
    parser->buffer += size;
    parser->length -= size;
    return value;
}
__declspec(dllexport) int BeaconDataInt(datap *parser) {
    int value;
    if (!parser || parser->length < 4) return 0;
    value = (int32_t)be32((const unsigned char *)parser->buffer);
    parser->buffer += 4; parser->length -= 4; return value;
}
__declspec(dllexport) short BeaconDataShort(datap *parser) {
    short value;
    if (!parser || parser->length < 2) return 0;
    value = (short)be16((const unsigned char *)parser->buffer);
    parser->buffer += 2; parser->length -= 2; return value;
}
__declspec(dllexport) char *BeaconDataExtract(datap *parser, int *size) {
    uint32_t length;
    char *value;
    if (size) *size = 0;
    if (!parser || parser->length < 4) return NULL;
    length = be32((const unsigned char *)parser->buffer);
    if (length > (uint32_t)(parser->length - 4)) return NULL;
    parser->buffer += 4; parser->length -= 4;
    value = parser->buffer; parser->buffer += length; parser->length -= (int)length;
    if (size) *size = (int)length;
    return value;
}
__declspec(dllexport) int BeaconDataLength(datap *parser) { return parser ? parser->length : 0; }

__declspec(dllexport) void BeaconFormatAlloc(formatp *format, int max) {
    if (!format) return;
    memset(format, 0, sizeof(*format));
    if (max <= 0 || max > 1024 * 1024) return;
    format->original = (char *)HeapAlloc(GetProcessHeap(), HEAP_ZERO_MEMORY, (size_t)max);
    if (format->original) { format->buffer = format->original; format->size = max; }
}
__declspec(dllexport) void BeaconFormatReset(formatp *format) {
    if (!format || !format->original) return;
    SecureZeroMemory(format->original, (size_t)format->size);
    format->buffer = format->original; format->length = 0;
}
__declspec(dllexport) void BeaconFormatFree(formatp *format) {
    if (!format) return;
    if (format->original) { SecureZeroMemory(format->original, (size_t)format->size); HeapFree(GetProcessHeap(), 0, format->original); }
    memset(format, 0, sizeof(*format));
}
__declspec(dllexport) void BeaconFormatAppend(formatp *format, const char *data, int length) {
    if (!format || !format->original || !data || length < 0 || format->length > format->size || length > format->size - format->length) return;
    memcpy(format->original + format->length, data, (size_t)length);
    format->length += length; format->buffer = format->original + format->length;
}
__declspec(dllexport) void BeaconFormatPrintf(formatp *format, const char *pattern, ...) {
    va_list args;
    int needed;
    char *buffer;
    if (!format || !pattern) return;
    va_start(args, pattern); needed = _vscprintf(pattern, args); va_end(args);
    if (needed < 0 || needed > 1024 * 1024) return;
    buffer = (char *)HeapAlloc(GetProcessHeap(), 0, (size_t)needed + 1); if (!buffer) return;
    va_start(args, pattern); vsnprintf(buffer, (size_t)needed + 1, pattern, args); va_end(args);
    BeaconFormatAppend(format, buffer, needed); HeapFree(GetProcessHeap(), 0, buffer);
}
__declspec(dllexport) char *BeaconFormatToString(formatp *format, int *size) {
    if (size) *size = format ? format->length : 0;
    return format ? format->original : NULL;
}
__declspec(dllexport) void BeaconFormatInt(formatp *format, int value) {
    unsigned char bytes[4]; put32(bytes, (uint32_t)value);
    BeaconFormatAppend(format, (const char *)bytes, 4);
}
