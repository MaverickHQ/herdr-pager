/* herdr-pager: app_main brings the kit up, builds the screen's bands and starts the event pump. */
#include "esp_log.h"
#include "lcd349.h"
#include "sdkconfig.h"
#if !CONFIG_IDF_TARGET_LINUX
#include "esp_heap_caps.h"
#endif

#include "pump.h"
#include "ssh_link.h"
#include "ui.h"

static const char *TAG = "pager";

void app_main(void)
{
    lcd349_err_t err = lcd349_init(NULL);
    ESP_LOGI(TAG, "herdr-pager %s: lcd349_init %s, status 0x%03x, libssh %s", PAGER_FW_VERSION,
             lcd349_err_str(err), (unsigned)lcd349_board_status(), ssh_link_library_version());
    if (lcd349_display()) {
        lcd349_ui_lock();
        ui_init();
        lcd349_ui_unlock();
    }
    pump_start();
#if !CONFIG_IDF_TARGET_LINUX
    /* The board's headroom once every task is up: internal RAM holds the stacks, Wi-Fi's and the link's buffers. */
    ESP_LOGI(TAG, "free: internal %u B (largest block %u B), PSRAM %u B",
             (unsigned)heap_caps_get_free_size(MALLOC_CAP_INTERNAL),
             (unsigned)heap_caps_get_largest_free_block(MALLOC_CAP_INTERNAL),
             (unsigned)heap_caps_get_free_size(MALLOC_CAP_SPIRAM));
#endif
}
