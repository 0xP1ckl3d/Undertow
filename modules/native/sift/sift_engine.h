#ifndef SIFT_ENGINE_H
#define SIFT_ENGINE_H

#define PCRE2_CODE_UNIT_WIDTH 8
#include "pcre2/src/pcre2.h"
#include "generated_rules.h"
#include "sift_validators.h"
#include <math.h>

typedef struct {
    pcre2_code *compiled[SIFT_PATTERN_COUNT];
    pcre2_match_context *context;
    pcre2_match_data *data;
    unsigned char matched[SIFT_RULE_COUNT];
    unsigned char reported[SIFT_RULE_COUNT];
    unsigned char seen_count[SIFT_RULE_COUNT];
    uint64_t seen_offsets[SIFT_RULE_COUNT][128];
} sift_engine;

static sift_engine engine;

static void sift_engine_init(const undertow_native_api_v1 *api, sift_stats *s) {
    size_t i;
    engine.context = pcre2_match_context_create(NULL);
    engine.data = pcre2_match_data_create(4, NULL);
    if (engine.context) {
        pcre2_set_match_limit(engine.context, 100000);
        pcre2_set_depth_limit(engine.context, 10000);
    }
    if (!engine.context || !engine.data) { s->errors++; return; }
    for (i = 0; i < SIFT_PATTERN_COUNT; ++i) {
        const sift_pattern *p = &sift_patterns[i];
        int error;
        PCRE2_SIZE offset;
        uint32_t flags;
        if (!p->regex || !sift_rules[p->rule].enabled) continue;
        flags = p->case_sensitive ? 0 : PCRE2_CASELESS;
        engine.compiled[i] = pcre2_compile((PCRE2_SPTR)p->pattern, PCRE2_ZERO_TERMINATED,
                                            flags, &error, &offset, NULL);
        if (!engine.compiled[i]) {
            outf(api, 1, "[!] rule pattern compile failed: %s at %llu (PCRE2 %d)\n",
                 sift_rules[p->rule].name, (unsigned long long)offset, error);
            s->errors++;
        }
    }
}

static void sift_engine_free(void) {
    size_t i;
    for (i = 0; i < SIFT_PATTERN_COUNT; ++i) {
        if (engine.compiled[i]) pcre2_code_free(engine.compiled[i]);
        engine.compiled[i] = NULL;
    }
    if (engine.data) pcre2_match_data_free(engine.data);
    if (engine.context) pcre2_match_context_free(engine.context);
    engine.data = NULL;
    engine.context = NULL;
}

static int sift_equal(const char *a, const char *b, int sensitive) {
    return sensitive ? strcmp(a, b) == 0 : _stricmp(a, b) == 0;
}

static int sift_prefix(const char *text, const char *prefix, int sensitive) {
    size_t n = strlen(prefix);
    return sensitive ? strncmp(text, prefix, n) == 0 : _strnicmp(text, prefix, n) == 0;
}

static int sift_glob(const char *pattern, const char *text) {
    while (*pattern) {
        if (*pattern == '*') {
            ++pattern;
            if (!*pattern) return 1;
            do { if (sift_glob(pattern, text)) return 1; } while (*text++);
            return 0;
        }
        if (!*text) return 0;
        if (*pattern != '?' && tolower((unsigned char)*pattern) != tolower((unsigned char)*text)) return 0;
        ++pattern; ++text;
    }
    return !*text;
}

static int sift_rule_excluded(const sift_rule *rule, const char *path) {
    const char *start = rule->exclude;
    while (start && *start) {
        const char *end = strchr(start, '|');
        size_t length = end ? (size_t)(end - start) : strlen(start);
        char pattern[1024];
        if (length < sizeof(pattern)) {
            memcpy(pattern, start, length);
            pattern[length] = '\0';
            if (sift_glob(pattern, path)) return 1;
        }
        if (!end) break;
        start = end + 1;
    }
    return 0;
}

static const char *sift_leaf(const char *path) {
    const char *a = strrchr(path, '\\'), *b = strrchr(path, '/');
    const char *last = a && b ? (a > b ? a : b) : (a ? a : b);
    return last ? last + 1 : path;
}

static const char *sift_extension(const char *leaf) {
    const char *dot = strrchr(leaf, '.');
    return dot ? dot : "";
}

static int sift_extension_allowed(const char *list, const char *ext) {
    const char *p = list;
    if (!list || !*list) return !*ext;
    while (1) {
        const char *end = strchr(p, '|');
        size_t n = end ? (size_t)(end - p) : strlen(p);
        if (strlen(ext) == n && _strnicmp(p, ext, n) == 0) return 1;
        if (!end) break;
        p = end + 1;
    }
    return 0;
}

static int sift_keywords_present(const sift_pattern *pattern, const unsigned char *buf, size_t len) {
    int i;
    if (!pattern->keyword_count) return 1;
    for (i = 0; i < pattern->keyword_count; ++i) {
        const char *word = sift_keywords[pattern->keyword_start + i];
        size_t n = strlen(word), p;
        if (n > len) continue;
        for (p = 0; p + n <= len; ++p) {
            if (pattern->case_sensitive ? memcmp(buf + p, word, n) == 0 :
                _strnicmp((const char *)buf + p, word, n) == 0) return 1;
        }
    }
    return 0;
}

static int sift_entropy_ok(const unsigned char *buf, size_t len, double threshold) {
    size_t counts[256] = {0}, i;
    double entropy = 0;
    if (threshold <= 0) return 1;
    if (!len) return 0;
    for (i = 0; i < len; ++i) counts[buf[i]]++;
    for (i = 0; i < 256; ++i) {
        if (counts[i]) {
            double p = (double)counts[i] / (double)len;
            entropy -= p * (log(p) / log(2.0));
        }
    }
    return entropy >= threshold;
}

static int sift_regex_match(size_t index, const unsigned char *subject, size_t length,
                            size_t start, size_t *begin, size_t *end) {
    int rc;
    PCRE2_SIZE *ov;
    if (!engine.compiled[index] || !engine.data || !engine.context) return 0;
    rc = pcre2_match(engine.compiled[index], subject, length, start, 0, engine.data, engine.context);
    if (rc < 0) return 0;
    ov = pcre2_get_ovector_pointer(engine.data);
    *begin = (size_t)ov[0]; *end = (size_t)ov[1];
    return 1;
}

static int sift_ignore_path(const char *path, int directory) {
    const char *leaf = sift_leaf(path), *ext = sift_extension(leaf);
    size_t i;
    for (i = 0; i < SIFT_IGNORE_COUNT; ++i) {
        const sift_ignore *r = &sift_ignores[i];
        if (!r->enabled) continue;
        if (sift_equal(r->target, "FileName", 0) && !directory && sift_glob(r->pattern, leaf)) return 1;
        if (sift_equal(r->target, "FileExtension", 0) && !directory && sift_glob(r->pattern, ext)) return 1;
        if (sift_equal(r->target, "DirectoryName", 0) && directory && sift_glob(r->pattern, leaf)) return 1;
        if (sift_equal(r->target, "DirectoryPath", 0) && sift_glob(r->pattern, path)) return 1;
        if (sift_equal(r->target, "Content", 0) && sift_glob(r->pattern, path)) return 1;
    }
    return 0;
}

static void sift_begin_file(void) {
    memset(engine.matched, 0, sizeof(engine.matched));
    memset(engine.reported, 0, sizeof(engine.reported));
    memset(engine.seen_count, 0, sizeof(engine.seen_count));
}

static int sift_rule_ready(const sift_rule *r) {
    return r->enabled && (r->parent < 0 || engine.matched[r->parent]);
}

static int sift_metadata_pattern(size_t index, const char *subject) {
    const sift_pattern *p = &sift_patterns[index];
    size_t begin, end;
    if (p->regex) return sift_regex_match(index, (const unsigned char *)subject, strlen(subject), 0, &begin, &end);
    if (sift_equal(p->target, "DirectoryPath", 0)) return sift_prefix(subject, p->pattern, p->case_sensitive);
    return sift_equal(subject, p->pattern, p->case_sensitive);
}

static void sift_scan_metadata(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s,
                               const wchar_t *path, const char *target) {
    char path8[8192];
    const char *leaf, *ext, *subject;
    size_t i;
    if (!wide_to_utf8(path, path8, sizeof(path8))) { s->errors++; return; }
    leaf = sift_leaf(path8); ext = sift_extension(leaf);
    for (i = 0; i < SIFT_PATTERN_COUNT; ++i) {
        const sift_pattern *p = &sift_patterns[i];
        const sift_rule *r = &sift_rules[p->rule];
        if (!sift_equal(p->target, target, 0) || !sift_rule_ready(r) ||
            sift_rule_excluded(r, path8)) continue;
        subject = sift_equal(target, "FileName", 0) || sift_equal(target, "DirectoryName", 0) ? leaf :
                  sift_equal(target, "FileExtension", 0) ? ext : path8;
        if (sift_equal(target, "FileExtension", 0)) {
            const char *needle = p->pattern;
            while (*needle == '*') ++needle;
            if (!sift_equal(ext, needle, p->case_sensitive)) continue;
        } else if (!sift_metadata_pattern(i, subject)) continue;
        engine.matched[p->rule] = 1;
        if (r->report && !engine.reported[p->rule]) {
            report_finding(api, o, s, r->severity, r->name, path, "metadata match");
            engine.reported[p->rule] = 1;
        }
    }
}

static int sift_content_candidate(const wchar_t *path) {
    char path8[8192];
    const char *ext;
    size_t i;
    if (!wide_to_utf8(path, path8, sizeof(path8))) return 0;
    ext = sift_extension(sift_leaf(path8));
    for (i = 0; i < SIFT_PATTERN_COUNT; ++i)
        if (sift_equal(sift_patterns[i].target, "Content", 0) &&
            sift_extension_allowed(sift_patterns[i].extensions, ext) &&
            sift_rule_ready(&sift_rules[sift_patterns[i].rule])) return 1;
    return 0;
}

static void sift_scan_buffer(const undertow_native_api_v1 *api, const sift_options *o, sift_stats *s,
                             const wchar_t *path, const unsigned char *buf, size_t len,
                             uint64_t base_offset, uint32_t stride) {
    char path8[8192];
    const char *ext;
    size_t i;
    if (!wide_to_utf8(path, path8, sizeof(path8))) { s->errors++; return; }
    ext = sift_extension(sift_leaf(path8));
    for (i = 0; i < SIFT_PATTERN_COUNT; ++i) {
        const sift_pattern *p = &sift_patterns[i];
        const sift_rule *r = &sift_rules[p->rule];
        size_t cursor = 0, begin, end;
        if (s->finding_limit_hit || output_limited || api_cancelled(api)) return;
        if (!sift_equal(p->target, "Content", 0) || !sift_rule_ready(r) ||
            sift_rule_excluded(r, path8) ||
            !sift_extension_allowed(p->extensions, ext) ||
            !sift_keywords_present(p, buf, len)) continue;
        while (cursor < len && !s->finding_limit_hit && !output_limited) {
            if (p->regex) {
                if (!sift_regex_match(i, buf, len, cursor, &begin, &end)) break;
            } else {
                size_t n = strlen(p->pattern), k;
                for (k = cursor; k + n <= len; ++k)
                    if (p->case_sensitive ? memcmp(buf + k, p->pattern, n) == 0 :
                        _strnicmp((const char *)buf + k, p->pattern, n) == 0) break;
                if (k + n > len) break;
                begin = k; end = k + n;
            }
            {
                int validation = sift_validate(r->validator, buf + begin, end - begin);
                size_t evidence_end = end;
                uint64_t absolute = base_offset + (uint64_t)begin * stride;
                if (validation == 0 || !sift_entropy_ok(buf + begin, end - begin, r->entropy)) {
                    cursor = end > cursor ? end : cursor + 1;
                    continue;
                }
                engine.matched[p->rule] = 1;
                if (strcmp(r->name, "Generic Secret Assignment") == 0) {
                    while (evidence_end < len && evidence_end - begin < MAX_EVIDENCE_BYTES &&
                           buf[evidence_end] != '\r' && buf[evidence_end] != '\n' &&
                           buf[evidence_end] != '"' && buf[evidence_end] != '\'' &&
                           buf[evidence_end] != ';' && buf[evidence_end] != ',') ++evidence_end;
                    if (evidence_end < len && (buf[evidence_end] == '"' || buf[evidence_end] == '\'')) ++evidence_end;
                }
                if (r->report && end > begin) {
                    size_t seen;
                    for (seen = 0; seen < engine.seen_count[p->rule]; ++seen)
                        if (engine.seen_offsets[p->rule][seen] == absolute) break;
                    if (seen == engine.seen_count[p->rule]) {
                        report_match(api, o, s, r->severity, r->name, path, buf + begin,
                                     evidence_end - begin, absolute, r->validator, validation == 1);
                        if (engine.seen_count[p->rule] < 128)
                            engine.seen_offsets[p->rule][engine.seen_count[p->rule]++] = absolute;
                    }
                }
            }
            cursor = end > cursor ? end : cursor + 1;
        }
    }
}

#endif
