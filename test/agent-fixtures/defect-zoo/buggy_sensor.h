// buggy_sensor — public header intentionally seeded with defects for firmware-reviewer testing.
// Each defect is tagged with its class for manual verification.

// DEFECT 1: include-guard — #ifndef used instead of #pragma once
#ifndef BUGGY_SENSOR_H  // DEFECT 1: include-guard (must be #pragma once)
#define BUGGY_SENSOR_H

#include <stdint.h>

// DEFECT 2: platform-type-leak — esp_http_server.h pulled directly into a public header
// (platform includes belong inside #ifdef ESP_PLATFORM guards or private headers)
#include "esp_http_server.h"  // DEFECT 2: platform-type-leak (platform include in public header)
#include "esp_err.h"          // DEFECT 2: platform-type-leak (platform include in public header)

typedef struct {
    uint32_t value;
    uint64_t timestamp_ms;
} buggy_sensor_sample_t;

// DEFECT 2: platform-type-leak — esp_err_t and httpd_handle_t are ESP-IDF types in public API;
// library code must return a library-defined error type and accept opaque handles.
esp_err_t buggy_sensor_init(httpd_handle_t server);  // DEFECT 2: platform-type-leak (esp_err_t, httpd_handle_t)
esp_err_t buggy_sensor_read(buggy_sensor_sample_t *out);  // DEFECT 2: platform-type-leak (esp_err_t)
esp_err_t buggy_sensor_deinit(void);  // DEFECT 2: platform-type-leak (esp_err_t)

#endif /* BUGGY_SENSOR_H */
