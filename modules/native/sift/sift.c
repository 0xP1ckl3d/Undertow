#define UNICODE
#define _UNICODE

#include <windows.h>
#include <lm.h>
#include <lmjoin.h>
#include <stdint.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>
#include <ctype.h>

#include "undertow_native.h"

#define SIFT_VERSION "0.2.0"
#define HEAD_BYTES (256u * 1024u)
#define TAIL_BYTES (256u * 1024u)
#define MAX_PATH_CHARS 4096
#define MAX_EVIDENCE_BYTES 1024
#define OUTPUT_BUDGET_BYTES (3u * 1024u * 1024u)

#ifndef STYPE_MASK
#define STYPE_MASK 0x000000FF
#endif

typedef struct sift_options {
    uint64_t max_files;
    uint64_t max_findings;
    uint64_t max_read_bytes;
    uint32_t max_depth;
    uint32_t max_hosts;
    int include_admin_shares;
    int enum_only;
    int json;
    wchar_t share[256];
} sift_options;

typedef struct sift_stats {
    uint64_t roots;
    uint64_t dirs;
    uint64_t files;
    uint64_t bytes_read;
    uint64_t findings;
    uint64_t errors;
    uint64_t hosts;
    uint64_t shares;
    int cancelled;
    int finding_limit_hit;
    int file_limit_hit;
    int read_limit_hit;
    int depth_limit_hit;
    int host_limit_hit;
} sift_stats;

static size_t output_bytes;
static int output_limited;
static size_t output_budget = OUTPUT_BUDGET_BYTES;

static int api_cancelled(const undertow_native_api_v1 *api) {
    return api && api->cancelled && api->cancelled(api->context) != 0;
}

static void out_text(const undertow_native_api_v1 *api, int error, const char *text) {
    size_t n;
    if (!api || !text) return;
    n = strlen(text);
    if (output_bytes + n > output_budget) { output_limited = 1; return; }
    if (error) {
        if (api->write_error && api->write_error(api->context, text, n) == 0) output_bytes += n;
        else output_limited = 1;
    } else {
        if (api->write && api->write(api->context, text, n) == 0) output_bytes += n;
        else output_limited = 1;
    }
}

static void outf(const undertow_native_api_v1 *api, int error, const char *fmt, ...) {
    char buffer[32768];
    va_list args;
    int n;
    va_start(args, fmt);
    n = vsnprintf(buffer, sizeof(buffer), fmt, args);
    va_end(args);
    if (n <= 0) return;
    if ((size_t)n >= sizeof(buffer)) n = (int)sizeof(buffer) - 1;
    if (output_bytes + (size_t)n > output_budget) { output_limited = 1; return; }
    if (error) {
        if (api->write_error && api->write_error(api->context, buffer, (size_t)n) == 0) output_bytes += (size_t)n;
        else output_limited = 1;
    } else {
        if (api->write && api->write(api->context, buffer, (size_t)n) == 0) output_bytes += (size_t)n;
        else output_limited = 1;
    }
}

static int utf8_to_wide(const char *src, wchar_t *dst, size_t dst_count) {
    int n;
    if (!src || !dst || dst_count == 0) return 0;
    n = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, src, -1, dst, (int)dst_count);
    return n > 0;
}

static int wide_to_utf8(const wchar_t *src, char *dst, size_t dst_count) {
    int n;
    if (!src || !dst || dst_count == 0) return 0;
    n = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, src, -1, dst, (int)dst_count, NULL, NULL);
    return n > 0;
}

static int arg_to_utf8(const void *args, size_t args_len, uint32_t index, char *dst, size_t dst_count) {
    undertow_native_span span;
    if (!dst || dst_count == 0) return 0;
    if (undertow_native_arg(args, args_len, index, &span) != 0) return 0;
    if ((size_t)span.length >= dst_count) return 0;
    memcpy(dst, span.data, span.length);
    dst[span.length] = '\0';
    return 1;
}

static uint32_t argc_from_buffer(const void *args, size_t args_len) {
    const uint8_t *p = (const uint8_t *)args;
    if (!p || args_len < 4) return 0;
    return undertow_native_le32(p);
}

static int parse_u64(const char *s, uint64_t *out) {
    unsigned long long value;
    char *end = NULL;
    if (!s || !*s || !out) return 0;
    value = strtoull(s, &end, 10);
    if (!end || *end != '\0') return 0;
    *out = (uint64_t)value;
    return 1;
}

static int parse_u32(const char *s, uint32_t *out) {
    uint64_t value;
    if (!parse_u64(s, &value) || value > 0xffffffffULL) return 0;
    *out = (uint32_t)value;
    return 1;
}

static void default_options(sift_options *o) {
    memset(o, 0, sizeof(*o));
    o->max_files = UINT64_MAX;
    o->max_findings = UINT64_MAX;
    o->max_read_bytes = UINT64_MAX;
    o->max_depth = UINT32_MAX;
    o->max_hosts = UINT32_MAX;
}

static void print_help(const undertow_native_api_v1 *api) {
    out_text(api, 0,
        "sift-native " SIFT_VERSION " - Undertow native sensitive-data discovery\n\n"
        "Usage:\n"
        "  run-native sift.module local PATH [options]\n"
        "  run-native sift.module network --device HOST [--share NAME] [options]\n"
        "  run-native sift.module domain [options]\n\n"
        "Options:\n"
        "  --enum-only             Enumerate hosts/shares without reading files\n"
        "  --admin-shares          Include shares ending in $\n"
        "  --max-files N           Stop after N files (unlimited by default)\n"
        "  --max-findings N        Stop after N findings (unlimited by default)\n"
        "  --max-read-mib N        Maximum content read in MiB (unlimited by default)\n"
        "  --max-depth N           Maximum directory recursion depth (unlimited by default)\n"
        "  --max-hosts N           Domain host limit (unlimited by default)\n"
        "  --json                  Emit findings and summary as JSON lines\n"
        "  --help                  Show this help\n\n"
        "Content findings include the matched value and its byte offset. Treat output as sensitive.\n"
        "SMB/domain modes use the agent process's current Windows security context.\n");
}

static int json_escape(const char *src, char *dst, size_t dst_count) {
    size_t i = 0, o = 0;
    if (!src || !dst || dst_count == 0) return 0;
    while (src[i] != '\0') {
        unsigned char c = (unsigned char)src[i++];
        const char *esc = NULL;
        char tmp[7];
        if (c == '"') esc = "\\\"";
        else if (c == '\\') esc = "\\\\";
        else if (c == '\b') esc = "\\b";
        else if (c == '\f') esc = "\\f";
        else if (c == '\n') esc = "\\n";
        else if (c == '\r') esc = "\\r";
        else if (c == '\t') esc = "\\t";
        else if (c < 0x20) {
            snprintf(tmp, sizeof(tmp), "\\u%04x", c);
            esc = tmp;
        }
        if (esc) {
            size_t n = strlen(esc);
            if (o + n + 1 > dst_count) return 0;
            memcpy(dst + o, esc, n);
            o += n;
        } else {
            if (o + 2 > dst_count) return 0;
            dst[o++] = (char)c;
        }
    }
    dst[o] = '\0';
    return 1;
}

static int json_escape_bytes(const unsigned char *src, size_t length, char *dst, size_t dst_count) {
    size_t i = 0, o = 0;
    while (i < length) {
        unsigned char c = src[i];
        const char *escape = NULL;
        char unicode[7];
        size_t n = 1, j;
        if (c == '"') escape = "\\\"";
        else if (c == '\\') escape = "\\\\";
        else if (c == '\n') escape = "\\n";
        else if (c == '\r') escape = "\\r";
        else if (c == '\t') escape = "\\t";
        else if (c < 0x20) {
            snprintf(unicode, sizeof(unicode), "\\u%04x", c);
            escape = unicode;
        } else if (c >= 0x80) {
            if (c >= 0xc2 && c <= 0xdf) n = 2;
            else if (c >= 0xe0 && c <= 0xef) n = 3;
            else if (c >= 0xf0 && c <= 0xf4) n = 4;
            if (n == 1 || i + n > length) n = 0;
            else for (j = 1; j < n; ++j)
                if ((src[i + j] & 0xc0) != 0x80) { n = 0; break; }
            if (!n) {
                snprintf(unicode, sizeof(unicode), "\\u%04x", c);
                escape = unicode;
                n = 1;
            }
        }
        if (escape) {
            size_t m = strlen(escape);
            if (o + m + 1 > dst_count) return 0;
            memcpy(dst + o, escape, m); o += m; ++i;
        } else {
            if (o + n + 1 > dst_count) return 0;
            memcpy(dst + o, src + i, n); o += n; i += n;
        }
    }
    dst[o] = '\0';
    return 1;
}

static void report_finding(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s,
                           const char *severity, const char *rule, const wchar_t *path, const char *detail) {
    char path8[8192];
    char pathj[16384];
    char detailj[2048];
    if (s->findings >= o->max_findings) { s->finding_limit_hit = 1; return; }
    if (!wide_to_utf8(path, path8, sizeof(path8))) strcpy_s(path8, sizeof(path8), "<path conversion failed>");
    s->findings++;
    if (o->json) {
        if (!json_escape(path8, pathj, sizeof(pathj))) strcpy_s(pathj, sizeof(pathj), "<path too long>");
        if (!json_escape(detail ? detail : "", detailj, sizeof(detailj))) strcpy_s(detailj, sizeof(detailj), "");
        outf(api, 0, "{\"type\":\"finding\",\"severity\":\"%s\",\"rule\":\"%s\",\"path\":\"%s\",\"detail\":\"%s\"}\n",
             severity, rule, pathj, detailj);
    } else {
        if (detail && *detail)
            outf(api, 0, "[%s] %-24s %s  (%s)\n", severity, rule, path8, detail);
        else
            outf(api, 0, "[%s] %-24s %s\n", severity, rule, path8);
    }
    if (output_limited) s->findings--;
}

static void report_match(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s,
                         const char *severity, const char *rule, const wchar_t *path,
                         const unsigned char *buf, size_t length, uint64_t offset,
                         const char *validator, int validated) {
    char path8[8192], pathj[16384], value[MAX_EVIDENCE_BYTES + 1], valuej[MAX_EVIDENCE_BYTES * 6 + 1];
    size_t n = length < MAX_EVIDENCE_BYTES ? length : MAX_EVIDENCE_BYTES;
    int truncated = length > n;
    if (s->findings >= o->max_findings) { s->finding_limit_hit = 1; return; }
    if (!wide_to_utf8(path, path8, sizeof(path8))) strcpy_s(path8, sizeof(path8), "<path conversion failed>");
    memcpy(value, buf, n);
    value[n] = '\0';
    if (!json_escape(path8, pathj, sizeof(pathj))) strcpy_s(pathj, sizeof(pathj), "<path too long>");
    if (!json_escape_bytes((const unsigned char *)value, n, valuej, sizeof(valuej))) return;
    s->findings++;
    if (o->json) {
        outf(api, 0, "{\"type\":\"finding\",\"severity\":\"%s\",\"rule\":\"%s\",\"path\":\"%s\",\"offset\":%llu,\"value\":\"%s\",\"truncated\":%s,\"validator\":\"%s\",\"validated\":%s}\n",
             severity, rule, pathj, (unsigned long long)offset, valuej, truncated ? "true" : "false",
             validator, validated ? "true" : "false");
    } else {
        outf(api, 0, "[%s] %-24s %s @%llu  %s%s%s\n", severity, rule, path8,
             (unsigned long long)offset, valuej, truncated ? " [truncated]" : "",
             validated ? "" : " [upstream validator not applied]");
    }
    if (output_limited) s->findings--;
}

#include "sift_engine.h"

static void scan_utf16_projection(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s,
                                  const wchar_t *path, const unsigned char *buf, size_t len, uint64_t base_offset) {
    unsigned char *ascii;
    size_t i, n;
    if (len < 4) return;
    n = len / 2;
    ascii = (unsigned char *)malloc(n);
    if (!ascii) return;
    for (i = 0; i < n; ++i) {
        unsigned char lo = buf[i * 2], hi = buf[i * 2 + 1];
        ascii[i] = hi == 0 ? lo : ' ';
    }
    sift_scan_buffer(api, o, s, path, ascii, n, base_offset, 2);
    free(ascii);
}

static void scan_content(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s, const wchar_t *path) {
    HANDLE h;
    LARGE_INTEGER size;
    DWORD got = 0;
    unsigned char *buf = NULL;
    size_t want, remain;
    if (s->bytes_read >= o->max_read_bytes) { s->read_limit_hit = 1; return; }
    if (api_cancelled(api)) return;
    h = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING,
                    FILE_ATTRIBUTE_NORMAL | FILE_FLAG_SEQUENTIAL_SCAN, NULL);
    if (h == INVALID_HANDLE_VALUE) { s->errors++; return; }
    if (!GetFileSizeEx(h, &size) || size.QuadPart < 0) { CloseHandle(h); s->errors++; return; }
    remain = (size_t)((o->max_read_bytes - s->bytes_read) > SIZE_MAX ? SIZE_MAX : (o->max_read_bytes - s->bytes_read));
    want = (size.QuadPart <= (LONGLONG)(HEAD_BYTES + TAIL_BYTES)) ? (size_t)size.QuadPart : (HEAD_BYTES + TAIL_BYTES);
    if (want > remain) { want = remain; s->read_limit_hit = 1; }
    if (want == 0) { CloseHandle(h); return; }
    buf = (unsigned char *)malloc(want);
    if (!buf) { CloseHandle(h); s->errors++; return; }

    if (size.QuadPart <= (LONGLONG)(HEAD_BYTES + TAIL_BYTES) || want <= HEAD_BYTES) {
        DWORD to_read = (DWORD)want;
        if (ReadFile(h, buf, to_read, &got, NULL) && got > 0) {
            s->bytes_read += got;
            sift_scan_buffer(api, o, s, path, buf, got, 0, 1);
            scan_utf16_projection(api, o, s, path, buf, got, 0);
        }
    } else {
        DWORD head_read = 0, tail_read = 0;
        DWORD head_want = HEAD_BYTES;
        DWORD tail_want = (DWORD)(want - HEAD_BYTES);
        if (ReadFile(h, buf, head_want, &head_read, NULL) && head_read > 0) {
            s->bytes_read += head_read;
            sift_scan_buffer(api, o, s, path, buf, head_read, 0, 1);
            scan_utf16_projection(api, o, s, path, buf, head_read, 0);
        }
        if (tail_want > 0 && size.QuadPart > tail_want) {
            LARGE_INTEGER pos;
            pos.QuadPart = size.QuadPart - tail_want;
            if (SetFilePointerEx(h, pos, NULL, FILE_BEGIN) && ReadFile(h, buf + HEAD_BYTES, tail_want, &tail_read, NULL) && tail_read > 0) {
                s->bytes_read += tail_read;
                sift_scan_buffer(api, o, s, path, buf + HEAD_BYTES, tail_read, (uint64_t)pos.QuadPart, 1);
                scan_utf16_projection(api, o, s, path, buf + HEAD_BYTES, tail_read, (uint64_t)pos.QuadPart);
            }
        }
    }
    free(buf);
    CloseHandle(h);
}

static void scan_one_file(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s, const wchar_t *path) {
    char path8[8192];
    if (s->files >= o->max_files) { s->file_limit_hit = 1; return; }
    if (s->finding_limit_hit || api_cancelled(api)) return;
    if (!wide_to_utf8(path, path8, sizeof(path8))) { s->errors++; return; }
    if (sift_ignore_path(path8, 0)) return;
    s->files++;
    sift_begin_file();
    sift_scan_metadata(api, o, s, path, "FileName");
    sift_scan_metadata(api, o, s, path, "FileExtension");
    sift_scan_metadata(api, o, s, path, "DirectoryPath");
    if (!o->enum_only && sift_content_candidate(path)) scan_content(api, o, s, path);
}

static int skip_directory(const wchar_t *name) {
    static const wchar_t *skip[] = {L".",L"..",L"$Recycle.Bin",L"System Volume Information",L"WinSxS",L"node_modules",L".cache",NULL};
    int i;
    for (i = 0; skip[i]; ++i) if (_wcsicmp(name, skip[i]) == 0) return 1;
    return 0;
}

static int join_path(wchar_t *out, size_t out_count, const wchar_t *base, const wchar_t *name) {
    size_t n;
    if (!out || !base || !name || out_count == 0) return 0;
    n = wcslen(base);
    if (n > 0 && (base[n - 1] == L'\\' || base[n - 1] == L'/'))
        return swprintf_s(out, out_count, L"%s%s", base, name) > 0;
    return swprintf_s(out, out_count, L"%s\\%s", base, name) > 0;
}

typedef struct sift_dir_frame {
    struct sift_dir_frame *parent;
    HANDLE find;
    WIN32_FIND_DATAW entry;
    wchar_t path[MAX_PATH_CHARS];
    uint32_t depth;
    int has_entry;
} sift_dir_frame;

static sift_dir_frame *open_directory(const undertow_native_api_v1 *api, const sift_options *o,
                                      sift_stats *s, const wchar_t *path, uint32_t depth,
                                      sift_dir_frame *parent) {
    wchar_t pattern[MAX_PATH_CHARS];
    sift_dir_frame *frame;
    size_t n = wcslen(path);
    if (n >= MAX_PATH_CHARS || swprintf_s(pattern, MAX_PATH_CHARS, L"%s%s*", path,
        n && (path[n - 1] == L'\\' || path[n - 1] == L'/') ? L"" : L"\\") <= 0) {
        s->errors++; return NULL;
    }
    frame = (sift_dir_frame *)calloc(1, sizeof(*frame));
    if (!frame) { s->errors++; return NULL; }
    if (wcscpy_s(frame->path, MAX_PATH_CHARS, path) != 0) { free(frame); s->errors++; return NULL; }
    frame->parent = parent;
    frame->depth = depth;
    s->dirs++;
    sift_begin_file();
    sift_scan_metadata(api, o, s, path, "DirectoryName");
    sift_scan_metadata(api, o, s, path, "DirectoryPath");
    if (s->finding_limit_hit || api_cancelled(api)) { free(frame); return NULL; }
    frame->find = FindFirstFileW(pattern, &frame->entry);
    if (frame->find == INVALID_HANDLE_VALUE) {
        if (GetLastError() != ERROR_FILE_NOT_FOUND) s->errors++;
        free(frame);
        return NULL;
    }
    frame->has_entry = 1;
    return frame;
}

static void scan_tree(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s, const wchar_t *root, uint32_t depth) {
    sift_dir_frame *top;
    if (api_cancelled(api)) { s->cancelled = 1; return; }
    if (depth > o->max_depth) { s->depth_limit_hit = 1; return; }
    if (s->files >= o->max_files) { s->file_limit_hit = 1; return; }
    top = open_directory(api, o, s, root, depth, NULL);
    while (top) {
        WIN32_FIND_DATAW entry;
        wchar_t child[MAX_PATH_CHARS];
        if (api_cancelled(api)) { s->cancelled = 1; break; }
        if (s->files >= o->max_files) { s->file_limit_hit = 1; break; }
        if (s->finding_limit_hit || output_limited) break;
        if (!top->has_entry) {
            sift_dir_frame *parent = top->parent;
            FindClose(top->find);
            free(top);
            top = parent;
            continue;
        }
        entry = top->entry;
        top->has_entry = FindNextFileW(top->find, &top->entry) != 0;
        if (!top->has_entry && GetLastError() != ERROR_NO_MORE_FILES) s->errors++;
        if (skip_directory(entry.cFileName)) continue;
        if (!join_path(child, MAX_PATH_CHARS, top->path, entry.cFileName)) { s->errors++; continue; }
        if (entry.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) {
            char child8[8192];
            sift_dir_frame *next;
            if (entry.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT) continue;
            if (wide_to_utf8(child, child8, sizeof(child8)) && sift_ignore_path(child8, 1)) continue;
            if (top->depth >= o->max_depth) { s->depth_limit_hit = 1; continue; }
            next = open_directory(api, o, s, child, top->depth + 1, top);
            if (next) top = next;
        } else {
            scan_one_file(api, o, s, child);
        }
    }
    while (top) {
        sift_dir_frame *parent = top->parent;
        FindClose(top->find);
        free(top);
        top = parent;
    }
}

static void emit_share(const undertow_native_api_v1 *api, const sift_options *o, const wchar_t *host, const wchar_t *share) {
    char h[1024], sh[1024], hj[2048], sj[2048];
    wide_to_utf8(host, h, sizeof(h));
    wide_to_utf8(share, sh, sizeof(sh));
    if (o->json) {
        json_escape(h, hj, sizeof(hj)); json_escape(sh, sj, sizeof(sj));
        outf(api, 0, "{\"type\":\"share\",\"host\":\"%s\",\"share\":\"%s\"}\n", hj, sj);
    } else {
        outf(api, 0, "[SHARE] \\\\%s\\%s\n", h, sh);
    }
}

static int is_admin_share(const wchar_t *share) {
    size_t n = wcslen(share);
    return n > 0 && share[n - 1] == L'$';
}

static void scan_share_root(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s,
                            const wchar_t *host, const wchar_t *share) {
    wchar_t root[MAX_PATH_CHARS];
    if (swprintf_s(root, MAX_PATH_CHARS, L"\\\\%s\\%s", host, share) <= 0) { s->errors++; return; }
    emit_share(api, o, host, share);
    s->shares++;
    sift_begin_file();
    sift_scan_metadata(api, o, s, share, "ShareName");
    if (!o->enum_only) { s->roots++; scan_tree(api, o, s, root, 0); }
}

static void scan_host_shares(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s, const wchar_t *host) {
    SHARE_INFO_1 *buffer = NULL;
    DWORD read = 0, total = 0, resume = 0;
    NET_API_STATUS status;
    wchar_t server[512];
    if (api_cancelled(api)) { s->cancelled = 1; return; }
    if (o->share[0]) { scan_share_root(api, o, s, host, o->share); return; }
    if (swprintf_s(server, 512, L"\\\\%s", host) <= 0) { s->errors++; return; }
    do {
        DWORD i;
        buffer = NULL; read = total = 0;
        status = NetShareEnum(server, 1, (LPBYTE *)&buffer, MAX_PREFERRED_LENGTH, &read, &total, &resume);
        if (status != NERR_Success && status != ERROR_MORE_DATA) {
            s->errors++;
            if (!o->json) {
                char host8[1024]; wide_to_utf8(host, host8, sizeof(host8));
                outf(api, 1, "[!] share enumeration failed for %s: %lu\n", host8, (unsigned long)status);
            }
            if (buffer) NetApiBufferFree(buffer);
            return;
        }
        for (i = 0; i < read; ++i) {
            DWORD type = buffer[i].shi1_type & STYPE_MASK;
            if (type != STYPE_DISKTREE) continue;
            if (!o->include_admin_shares && is_admin_share(buffer[i].shi1_netname)) continue;
            scan_share_root(api, o, s, host, buffer[i].shi1_netname);
            if (api_cancelled(api) || s->files >= o->max_files || s->finding_limit_hit || output_limited) break;
        }
        if (buffer) NetApiBufferFree(buffer);
        if (api_cancelled(api) || s->files >= o->max_files || s->finding_limit_hit || output_limited) break;
    } while (status == ERROR_MORE_DATA);
}

static int current_domain(wchar_t *domain, size_t count) {
    LPWSTR joined = NULL;
    NETSETUP_JOIN_STATUS status;
    NET_API_STATUS rc;
    rc = NetGetJoinInformation(NULL, &joined, &status);
    if (rc != NERR_Success || !joined || status != NetSetupDomainName) {
        if (joined) NetApiBufferFree(joined);
        return 0;
    }
    wcsncpy_s(domain, count, joined, _TRUNCATE);
    NetApiBufferFree(joined);
    return 1;
}

static void run_domain(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s) {
    wchar_t domain[512];
    SERVER_INFO_101 *buffer = NULL;
    DWORD read = 0, total = 0, resume = 0;
    NET_API_STATUS status;
    uint32_t processed = 0;
    if (!current_domain(domain, 512)) {
        out_text(api, 1, "[!] unable to identify a joined Active Directory domain\n");
        s->errors++;
        return;
    }
    if (!o->json) {
        char d[1024]; wide_to_utf8(domain, d, sizeof(d));
        outf(api, 0, "[*] Domain: %s\n", d);
    }
    do {
        DWORD i;
        buffer = NULL; read = total = 0;
        status = NetServerEnum(NULL, 101, (LPBYTE *)&buffer, MAX_PREFERRED_LENGTH, &read, &total,
                               SV_TYPE_WORKSTATION | SV_TYPE_SERVER, domain, &resume);
        if (status != NERR_Success && status != ERROR_MORE_DATA) {
            outf(api, 1, "[!] domain computer enumeration failed: %lu\n", (unsigned long)status);
            s->errors++;
            if (buffer) NetApiBufferFree(buffer);
            return;
        }
        for (i = 0; i < read && processed < o->max_hosts; ++i) {
            wchar_t *host = buffer[i].sv101_name;
            char host8[1024], hostj[2048];
            if (!host || !*host) continue;
            processed++; s->hosts++;
            wide_to_utf8(host, host8, sizeof(host8));
            if (o->json) {
                json_escape(host8, hostj, sizeof(hostj));
                outf(api, 0, "{\"type\":\"host\",\"host\":\"%s\"}\n", hostj);
            } else {
                if (o->max_hosts == UINT32_MAX) outf(api, 0, "[*] Host %u: %s\n", processed, host8);
                else outf(api, 0, "[*] Host %u/%u: %s\n", processed, o->max_hosts, host8);
            }
            scan_host_shares(api, o, s, host);
            if (api_cancelled(api) || s->files >= o->max_files || s->finding_limit_hit || output_limited) break;
        }
        if (buffer) NetApiBufferFree(buffer);
        if (processed >= o->max_hosts) { s->host_limit_hit = 1; break; }
        if (api_cancelled(api) || s->files >= o->max_files || s->finding_limit_hit || output_limited) break;
    } while (status == ERROR_MORE_DATA);
}

static void print_summary(const undertow_native_api_v1 *api, const sift_options *o, const sift_stats *s) {
    output_budget = OUTPUT_BUDGET_BYTES + 4096u;
    if (o->json) {
        outf(api, 0,
            "{\"type\":\"summary\",\"roots\":%llu,\"hosts\":%llu,\"shares\":%llu,\"directories\":%llu,\"files\":%llu,\"bytes_read\":%llu,\"findings\":%llu,\"errors\":%llu,\"cancelled\":%s,\"finding_limit_hit\":%s,\"file_limit_hit\":%s,\"read_limit_hit\":%s,\"depth_limit_hit\":%s,\"host_limit_hit\":%s,\"output_limit_hit\":%s}\n",
            (unsigned long long)s->roots, (unsigned long long)s->hosts, (unsigned long long)s->shares,
            (unsigned long long)s->dirs, (unsigned long long)s->files, (unsigned long long)s->bytes_read,
            (unsigned long long)s->findings, (unsigned long long)s->errors, s->cancelled ? "true" : "false",
            s->finding_limit_hit ? "true" : "false", s->file_limit_hit ? "true" : "false",
            s->read_limit_hit ? "true" : "false", s->depth_limit_hit ? "true" : "false",
            s->host_limit_hit ? "true" : "false", output_limited ? "true" : "false");
    } else {
        out_text(api, 0, "\nSummary\n-------\n");
        outf(api, 0, "Roots       : %llu\nHosts       : %llu\nShares      : %llu\nDirectories : %llu\nFiles       : %llu\nRead        : %.2f MiB\nFindings    : %llu\nErrors      : %llu\nCancelled   : %s\nFile cap    : %s\nRead cap    : %s\nDepth cap   : %s\nHost cap    : %s\nFinding cap : %s\nOutput cap  : %s\n",
             (unsigned long long)s->roots, (unsigned long long)s->hosts, (unsigned long long)s->shares,
             (unsigned long long)s->dirs, (unsigned long long)s->files,
             (double)s->bytes_read / (1024.0 * 1024.0), (unsigned long long)s->findings,
             (unsigned long long)s->errors, s->cancelled ? "yes" : "no",
             s->file_limit_hit ? "reached" : "no", s->read_limit_hit ? "reached" : "no",
             s->depth_limit_hit ? "reached" : "no", s->host_limit_hit ? "reached" : "no",
             s->finding_limit_hit ? "reached" : "no", output_limited ? "reached" : "no");
    }
}

static int parse_common_option(const char *arg, const char *next, sift_options *o, int *consume_next) {
    uint64_t v64;
    uint32_t v32;
    *consume_next = 0;
    if (strcmp(arg, "--enum-only") == 0) { o->enum_only = 1; return 1; }
    if (strcmp(arg, "--admin-shares") == 0) { o->include_admin_shares = 1; return 1; }
    if (strcmp(arg, "--json") == 0) { o->json = 1; return 1; }
    if (strcmp(arg, "--max-files") == 0 && next && parse_u64(next, &v64)) { o->max_files = v64; *consume_next = 1; return 1; }
    if (strcmp(arg, "--max-findings") == 0 && next && parse_u64(next, &v64) && v64 > 0) { o->max_findings = v64; *consume_next = 1; return 1; }
    if (strcmp(arg, "--max-read-mib") == 0 && next && parse_u64(next, &v64) && v64 <= UINT64_MAX / (1024ULL * 1024ULL)) { o->max_read_bytes = v64 * 1024ULL * 1024ULL; *consume_next = 1; return 1; }
    if (strcmp(arg, "--max-depth") == 0 && next && parse_u32(next, &v32)) { o->max_depth = v32; *consume_next = 1; return 1; }
    if (strcmp(arg, "--max-hosts") == 0 && next && parse_u32(next, &v32)) { o->max_hosts = v32; *consume_next = 1; return 1; }
    return 0;
}

__declspec(dllexport) int32_t undertow_main(const undertow_native_api_v1 *api, const void *args, size_t args_len) {
    uint32_t argc, i;
    char command[128] = {0};
    sift_options o;
    sift_stats s;
    default_options(&o);
    memset(&s, 0, sizeof(s));

    if (!api || api->version != UNDERTOW_NATIVE_ABI_VERSION || api->os != UNDERTOW_NATIVE_OS_WINDOWS || api->arch != UNDERTOW_NATIVE_ARCH_AMD64) return 2;
    argc = argc_from_buffer(args, args_len);
    if (argc == 0 || !arg_to_utf8(args, args_len, 0, command, sizeof(command)) || strcmp(command, "--help") == 0 || strcmp(command, "help") == 0) {
        print_help(api); return 0;
    }
    sift_engine_init(api, &s);

    if (strcmp(command, "local") == 0) {
        char path8[4096];
        wchar_t path[MAX_PATH_CHARS];
        if (argc < 2 || !arg_to_utf8(args, args_len, 1, path8, sizeof(path8)) || !utf8_to_wide(path8, path, MAX_PATH_CHARS)) {
            out_text(api, 1, "local requires a UTF-8 PATH\n"); print_help(api); return 2;
        }
        for (i = 2; i < argc; ++i) {
            char a[256], n[256]; int consume = 0;
            arg_to_utf8(args, args_len, i, a, sizeof(a));
            n[0] = '\0'; if (i + 1 < argc) arg_to_utf8(args, args_len, i + 1, n, sizeof(n));
            if (strcmp(a, "--help") == 0) { print_help(api); return 0; }
            if (!parse_common_option(a, i + 1 < argc ? n : NULL, &o, &consume)) { outf(api, 1, "unknown option: %s\n", a); return 2; }
            if (consume) ++i;
        }
        s.roots = 1;
        {
            DWORD attributes = GetFileAttributesW(path);
            if (attributes == INVALID_FILE_ATTRIBUTES) {
                outf(api, 1, "local path is not accessible: %lu\n", GetLastError());
                return 2;
            }
            if (attributes & FILE_ATTRIBUTE_DIRECTORY) scan_tree(api, &o, &s, path, 0);
            else scan_one_file(api, &o, &s, path);
        }
    } else if (strcmp(command, "network") == 0) {
        wchar_t host[512] = {0};
        for (i = 1; i < argc; ++i) {
            char a[256], n[4096]; int consume = 0;
            arg_to_utf8(args, args_len, i, a, sizeof(a));
            n[0] = '\0'; if (i + 1 < argc) arg_to_utf8(args, args_len, i + 1, n, sizeof(n));
            if (strcmp(a, "--help") == 0) { print_help(api); return 0; }
            if (strcmp(a, "--device") == 0 && i + 1 < argc && utf8_to_wide(n, host, 512)) { ++i; continue; }
            if (strcmp(a, "--share") == 0 && i + 1 < argc && utf8_to_wide(n, o.share, 256)) { ++i; continue; }
            if (!parse_common_option(a, i + 1 < argc ? n : NULL, &o, &consume)) { outf(api, 1, "unknown option: %s\n", a); return 2; }
            if (consume) ++i;
        }
        if (!host[0]) { out_text(api, 1, "network requires --device HOST\n"); return 2; }
        s.hosts = 1;
        scan_host_shares(api, &o, &s, host);
    } else if (strcmp(command, "domain") == 0) {
        for (i = 1; i < argc; ++i) {
            char a[256], n[256]; int consume = 0;
            arg_to_utf8(args, args_len, i, a, sizeof(a));
            n[0] = '\0'; if (i + 1 < argc) arg_to_utf8(args, args_len, i + 1, n, sizeof(n));
            if (strcmp(a, "--help") == 0) { print_help(api); return 0; }
            if (!parse_common_option(a, i + 1 < argc ? n : NULL, &o, &consume)) { outf(api, 1, "unknown option: %s\n", a); return 2; }
            if (consume) ++i;
        }
        run_domain(api, &o, &s);
    } else {
        outf(api, 1, "unknown command: %s\n", command);
        print_help(api);
        return 2;
    }

    if (api_cancelled(api)) s.cancelled = 1;
    print_summary(api, &o, &s);
    sift_engine_free();
    return s.cancelled ? 130 : 0;
}
