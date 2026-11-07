# Third-party licence texts

The release's archive carries these files in `licenses/`, beside `LICENSE` and `THIRD_PARTY.md` (`tools/release/pack`). Each is a verbatim copy of the licence file that ships with the component, for the software built into `pager-bridge` and the firmware's images. `THIRD_PARTY.md` says what each component is for.

| File | Component | Licence | Copied from |
|---|---|---|---|
| `go.txt` | Go: the runtime and standard library, in `pager-bridge` | BSD-3-Clause | Go 1.26.10's `LICENSE` |
| `golang.org-x-crypto.txt` | `golang.org/x/crypto` | BSD-3-Clause | the module's `LICENSE`, v0.58.1-0.20261009181718-2f1f834330b2 (the same text as v0.56.0's) |
| `golang.org-x-sys.txt` | `golang.org/x/sys` | BSD-3-Clause | the module's `LICENSE`, v0.49.0 (the same text as v0.47.0's) |
| `github.com-pelletier-go-toml-v2.txt` | `github.com/pelletier/go-toml/v2` | MIT | the module's `LICENSE`, v2.2.4 |
| `esp-idf.txt` | ESP-IDF | Apache-2.0 | ESP-IDF v5.5.6's `LICENSE` |
| `esp-idf-COPYRIGHT.rst` | The third-party software inside ESP-IDF (FreeRTOS, lwIP, mbedTLS, newlib and the rest), with their licences | various | ESP-IDF v5.5.6's `docs/en/COPYRIGHT.rst` |
| `lcd349-kit.txt` | lcd349-kit's `components/lcd349` | Apache-2.0 | lcd349-kit's `LICENSE` |
| `lvgl.txt` | `lvgl/lvgl` | MIT | LVGL 9.6.0's `LICENCE.txt` |
| `lvgl-font-montserrat-OFL.txt` | The Montserrat font in LVGL's built-in fonts | OFL-1.1 | LVGL 9.6.0's `scripts/generators/built_in_font/font_license/Montserrat/OFL.txt` |
| `lvgl-font-fontawesome5.txt` | The FontAwesome 5 symbols in LVGL's built-in fonts | Font Awesome Free (OFL-1.1 for the font) | LVGL 9.6.0's `scripts/generators/built_in_font/font_license/FontAwesome5/LICENSE.txt` |
| `libssh-COPYING.txt` | libssh 0.12.2 (`david-cermak/libssh`), the firmware's SSH client | LGPL-2.1 | the component's `libssh-mirror/COPYING` |
| `libssh-BSD.txt` | The parts of libssh under a BSD licence | BSD | the component's `libssh-mirror/BSD` |
| `espressif-sock_utils.txt` | `espressif/sock_utils` | Apache-2.0 | the component's `LICENSE`, 0.2.2 |
| `espressif-esp_lcd_axs15231b.txt` | `espressif/esp_lcd_axs15231b` | Apache-2.0 | the component's `license.txt`, 2.1.1 |
| `espressif-esp_codec_dev.txt` | `espressif/esp_codec_dev` | Apache-2.0 | the component's `LICENSE`, 1.6.2 |
| `espressif-esp_lcd_touch.txt` | `espressif/esp_lcd_touch` | Apache-2.0 | the component's `license.txt`, 1.2.1 |
| `esp-web-tools.txt` | `esp-web-tools` 10.4.0 (ESP Web Tools), the setup page's flasher, vendored in `bridge/internal/setup/web/vendor/` and embedded in `pager-bridge` | Apache-2.0 | the package's `LICENSE` (the vendored copy beside it) |
| `npm-material-web.txt` | `@material/web` 2.4.1, bundled in ESP Web Tools' browser build | Apache-2.0 | the package's `LICENSE` |
| `npm-esptool-js.txt` | `esptool-js` 0.6.0, bundled in ESP Web Tools' browser build | Apache-2.0 | the package's `LICENSE` |
| `npm-improv-wifi-serial-sdk.txt` | `improv-wifi-serial-sdk` 2.8.0, bundled in ESP Web Tools' browser build | Apache-2.0 | the package's `LICENSE` |
| `npm-lit.txt` | `lit` 3.3.3, `lit-element` 4.2.0, `lit-html` 3.3.0, `@lit/reactive-element` 2.1.0 and `@lit-labs/ssr-dom-shim` 1.2.0, bundled in ESP Web Tools' browser build | BSD-3-Clause | `lit`'s `LICENSE`; `lit-element`'s and `@lit/reactive-element`'s are the same text, `lit-html`'s too but for a final newline, and `@lit-labs/ssr-dom-shim` ships none (its `package.json` says BSD-3-Clause; it's from the same lit repository) |
| `npm-pako.txt` | `pako` 2.2.0 (`esptool-js`'s), bundled in ESP Web Tools' browser build | MIT AND Zlib: this is the MIT part | the package's `LICENSE` |
| `npm-pako-zlib.txt` | `pako` 2.2.0's port of zlib | Zlib | the notice at the head of the package's `lib/zlib/deflate.js`, without its `//` comment marks |
| `npm-atob-lite.txt` | `atob-lite` 2.0.0, bundled in ESP Web Tools' browser build | MIT | the package's `LICENSE.md` |
| `npm-tslib.txt` | `tslib` 2.8.1, bundled in ESP Web Tools' browser build | 0BSD | the package's `LICENSE.txt` |

libssh is LGPL-2.1: the release also carries libssh 0.12.2's full source, and this repository's source and build files are public, so the firmware can be relinked against a modified libssh (`docs/adr/0004`).

The npm packages are the ones in the vendored ESP Web Tools' `package-lock.json` (but `@types/trusted-types`, type declarations with no code), taken from the npm registry's tarballs, each checked against that lock's sha512 `integrity` (2026-11-07). The browser build keeps none of their licence comments, so these files are their notices. A test (`TestTheLicencesCoverTheSetupPagesBundle`, `tools/release/internal/release`) fails when a package in that lock has no row here or in `THIRD_PARTY.md`.

When a component is added, or its licence changes, copy its licence file here verbatim and add its row. `pack` refuses an empty file, and an empty folder.
