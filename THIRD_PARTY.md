# Third-party software

herdr-pager is licensed under Apache-2.0 (`LICENSE`). It uses the third-party software below. There's no GPL code in the firmware or the bridge, and LGPL only as a separate library (`docs/adr/0004`). Before adding to this list, run valvur `check_package`, and check the licence against ADR 0004.

## Firmware (`firmware/`), resolved by the ESP-IDF component manager

Exact versions are in the committed lock files, `firmware/dependencies.lock.esp32s3` and `dependencies.lock.linux` (design T13).

| Component | Version | Licence | Used for |
|---|---|---|---|
| ESP-IDF | v5.5.6 | Apache-2.0 | Build system, FreeRTOS, Wi-Fi, mbedTLS, drivers, the `linux` host target, and Unity for the host tests |
| lcd349-kit (`components/lcd349`) | v0.2.2 | Apache-2.0 | All hardware access: the panel, touch, buttons, tones, settings, and the emulator. From its public repository, `github.com/MaverickHQ/lcd349-kit`. |
| `lvgl/lvgl` | 9.6.0 | MIT | The UI |
| LVGL's bundled Montserrat fonts | with LVGL | OFL-1.1 (font), MIT (LVGL's conversion) | Every font on screen (`firmware/main/platform/fonts.c`) |
| `david-cermak/libssh` (libssh 0.12.2), `esp32s3` only | 0.12.2 | **LGPL-2.1** | The SSH client on the board. It's linked as a separately identifiable library. If you distribute a firmware binary, publish this repository's full source and build files alongside it, so that it can be relinked against a modified libssh (`docs/adr/0004`). |
| `espressif/sock_utils` (required by `david-cermak/libssh`) | 0.2.2 | Apache-2.0 | Socket helpers for libssh |
| `espressif/esp_lcd_axs15231b` (from the kit) | 2.1.1 | Apache-2.0 | The panel driver |
| `espressif/esp_codec_dev` (from the kit) | 1.6.2 | Apache-2.0 | The ES8311 codec, for tones |
| `espressif/esp_lcd_touch` (required by `esp_lcd_axs15231b`) | 1.2.1 | Apache-2.0 | Present but unused: the kit reads touch itself |
| `espressif/cmake_utilities` (required by `esp_lcd_axs15231b`) | 0.5.3 | Apache-2.0 | Build helpers |

### The emulator build (`linux` target)

| Library | Licence | How |
|---|---|---|
| libssh 0.12.2 (Homebrew) | LGPL-2.1 | Dynamically linked through `pkg-config`; not redistributed |
| OpenSSL 3 (Homebrew, libssh's crypto) | Apache-2.0 | Dynamically linked by libssh; not redistributed |

## Go: the bridge (`bridge/`)

Exact versions are in `bridge/go.sum`. `tools/measure` and `tools/runphase` use only Go's standard library; phase 0's spike tools (`spikes/sshd/` and `spikes/ssh_esp/helper/`) use `x/crypto` and `x/sys`, at the versions phase 0 used.

| Module | Licence | Used for |
|---|---|---|
| `golang.org/x/crypto` | BSD-3-Clause | `x/crypto/ssh`: the bridge's SSH server |
| `golang.org/x/sys` | BSD-3-Clause | `x/sys/unix`: the serial port, for pairing over USB |
| `github.com/pelletier/go-toml/v2` | MIT | The bridge's TOML files: its config, devices, keymaps and risk rules, and fakeherdr's scenarios |
| `github.com/creack/pty` | MIT | Tests only: a pseudo-terminal stands in for the pager's serial port |
| `golang.org/x/term`, `x/net`, `x/text` | BSD-3-Clause | In `go.sum` as `x/crypto`'s requirements; no package of theirs is built in |

## The setup page's flasher (`bridge/internal/setup/web/vendor/`), embedded in the bridge

The page `pager-bridge setup` serves flashes the pager in Chrome with ESP Web Tools' browser build, vendored and embedded in `pager-bridge` (its `SOURCE.md` says from where, how it was checked, and the one change: two static `style` attributes dropped for the page's CSP). That build bundles the npm packages below, at the versions in its `package-lock.json`, and keeps none of their licence comments: their licence texts are in `tools/release/licenses/`, and in `licenses/` in the release's archive.

| Package | Version | Licence | Used for |
|---|---|---|---|
| `esp-web-tools` (ESP Web Tools, by ESPHome) | 10.4.0 | Apache-2.0 | The install button and dialog: flashing and updating the pager over Web Serial |
| `esptool-js` | 0.6.0 | Apache-2.0 | Talks to the ESP32-S3's ROM bootloader and writes the images |
| `pako` (`esptool-js`'s) | 2.2.0 | MIT AND Zlib | Compresses the images on the way |
| `improv-wifi-serial-sdk` | 2.8.0 | Apache-2.0 | ESP Web Tools' Improv client (the pager doesn't speak Improv, so it finds nothing) |
| `@material/web` | 2.4.1 | Apache-2.0 | The dialog's buttons, lists and checkbox |
| `lit` | 3.3.3 | BSD-3-Clause | The dialog's web components, with the four below |
| `lit-element` | 4.2.0 | BSD-3-Clause | |
| `lit-html` | 3.3.0 | BSD-3-Clause | |
| `@lit/reactive-element` | 2.1.0 | BSD-3-Clause | |
| `@lit-labs/ssr-dom-shim` | 1.2.0 | BSD-3-Clause | In the lock as `@lit/reactive-element`'s; for server rendering, which the page doesn't do |
| `atob-lite` | 2.0.0 | MIT | Base64 decoding, for `esptool-js`'s flasher stubs |
| `tslib` | 2.8.1 | 0BSD | TypeScript's runtime helpers |

## Tools that build the repository's images (`tools/media/`)

These run on the maintainer's Mac and aren't part of the pager or the bridge.

| Software | Licence | How |
|---|---|---|
| Pillow (Python) | MIT-CMU | Composes `docs/images/` from the emulator's screenshots |
| macOS's Avenir Next and Menlo | Apple's system font licence | Set the captions and labels in the images. No font file is in the repository. |

The photo of the board in `docs/images/board.jpg` and `board-demo.gif` is the maintainer's own.

## Not used

Waveshare's demo repository for this board (github.com/waveshareteam/ESP32-S3-Touch-LCD-3.49) has no licence. No code from it is in this repository; facts read from it are cited where they're used. herdr-pager is not affiliated with Waveshare, herdr or Anthropic.
