#ifndef UNDERTOW_NATIVE_SHELL_H
#define UNDERTOW_NATIVE_SHELL_H

#include "undertow_native.h"

#ifdef __cplusplus
extern "C" {
#endif

#define UNDERTOW_NATIVE_SHELL_ABI_VERSION 1u

typedef int32_t (*undertow_native_read_fn)(uintptr_t context, void *bytes, size_t capacity, size_t *length);

/* The prefix intentionally matches undertow_native_api_v1 so a module can
   share its output bridge between one-shot and live entry points. */
typedef struct undertow_native_shell_api_v1 {
    uint32_t size;
    uint32_t version;
    uintptr_t context;
    undertow_native_write_fn write;
    undertow_native_write_fn write_error;
    undertow_native_state_fn cancelled;
    undertow_native_state_fn job_state;
    uint32_t os;
    uint32_t arch;
    uint32_t process_id;
    uint32_t reserved;
    undertow_native_read_fn read;
} undertow_native_shell_api_v1;

/* Optional live-shell entry point. Input is a raw terminal byte stream. */
__declspec(dllexport) int32_t undertow_shell_main(const undertow_native_shell_api_v1 *api);

#ifdef __cplusplus
}
#endif
#endif

