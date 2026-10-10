#pragma once

#include <stdarg.h>
#include <wchar.h>
#include "undertow_native.h"

void shellpower_set_api(const undertow_native_api_v1 *api);
int shellpower_printf(const wchar_t *format, ...);
int shellpower_errorf(const wchar_t *format, ...);

