#include <stdio.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>
#include <inttypes.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "nvs_flash.h"
#include "nvs.h"
#include "esp_log.h"
#include "esp_system.h"

static const char *TAG = "test-fw";

void app_main(void)
{
    // Boot: Initialize NVS
    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    // Print boot banner
    ESP_LOGI(TAG, "serial-io-test-firmware v1.0.0");

    // NVS seed: Check if initialized, seed on first boot
    nvs_handle_t nvs_handle;
    ret = nvs_open("test_ns", NVS_READWRITE, &nvs_handle);
    ESP_ERROR_CHECK(ret);

    uint8_t initialized = 0;
    ret = nvs_get_u8(nvs_handle, "initialized", &initialized);
    if (ret == ESP_ERR_NVS_NOT_FOUND) {
        // First boot: seed NVS entries
        ESP_ERROR_CHECK(nvs_set_u32(nvs_handle, "counter", 0));
        ESP_ERROR_CHECK(nvs_set_str(nvs_handle, "name", "serial-io-test"));
        ESP_ERROR_CHECK(nvs_set_u8(nvs_handle, "flag", 1));
        ESP_ERROR_CHECK(nvs_set_i16(nvs_handle, "temperature", 2500));
        ESP_ERROR_CHECK(nvs_set_u8(nvs_handle, "initialized", 1));
        ESP_ERROR_CHECK(nvs_commit(nvs_handle));
        ESP_LOGI(TAG, "NVS seeded with test entries");
    } else {
        ESP_ERROR_CHECK(ret);
        ESP_LOGI(TAG, "NVS already initialized");
    }

    nvs_close(nvs_handle);

    // Set stdin to non-blocking mode
    int flags = fcntl(STDIN_FILENO, F_GETFL, 0);
    fcntl(STDIN_FILENO, F_SETFL, flags | O_NONBLOCK);

    // Main loop
    uint32_t tick = 0;
    char s_line_buf[128];

    while (1) {
        ESP_LOGI(TAG, "[%"PRIu32"] counter=%"PRIu32" heap=%"PRIu32" tick", tick, tick, (uint32_t)esp_get_free_heap_size());

        // Every 10 ticks: persist counter to NVS
        if (tick % 10 == 0 && tick > 0) {
            ret = nvs_open("test_ns", NVS_READWRITE, &nvs_handle);
            ESP_ERROR_CHECK(ret);
            ESP_ERROR_CHECK(nvs_set_u32(nvs_handle, "counter", tick));
            ESP_ERROR_CHECK(nvs_commit(nvs_handle));
            nvs_close(nvs_handle);
            ESP_LOGI(TAG, "NVS counter persisted");
        }

        // Non-blocking stdin check
        if (fgets(s_line_buf, sizeof(s_line_buf), stdin) != NULL) {
            // Trim trailing newline
            size_t len = strlen(s_line_buf);
            if (len > 0 && s_line_buf[len - 1] == '\n') {
                s_line_buf[len - 1] = '\0';
            }
            ESP_LOGI(TAG, "echo: %s", s_line_buf);
        }

        vTaskDelay(pdMS_TO_TICKS(1000));
        tick++;
    }
}
