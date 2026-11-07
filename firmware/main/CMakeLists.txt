# The firmware takes its sources by directory, so no lane edits a build list (phase 2 design §Lanes):
#   app/        the app core: pure C, host-tested in firmware/test_host (design D2-1)
#   platform/   the glue: app_main, the event pump, the action executor, fonts, theme, hints
#   ui/         one file per screen, drawn from the core's render model
#   net/        ssh_link: the only file that includes an SSH library's headers (design D4)
#   prov/       the device's identity (its key pair)
#   testhooks/  @T lines, in test builds only (CONFIG_PAGER_TEST_HOOKS, design D2-4)
# Host tests (*_test.c) build only in firmware/test_host.
set(dirs app platform ui net prov testhooks)
# CONFIGURE_DEPENDS makes the build notice a new file; it's invalid in ESP-IDF's script-mode pass.
set(glob_opts)
if(NOT CMAKE_BUILD_EARLY_EXPANSION)
    set(glob_opts CONFIGURE_DEPENDS)
endif()
set(srcs)
foreach(dir ${dirs})
    file(GLOB dir_srcs ${glob_opts} "${CMAKE_CURRENT_LIST_DIR}/${dir}/*.c")
    list(APPEND srcs ${dir_srcs})
endforeach()
list(FILTER srcs EXCLUDE REGEX "_test\\.c$")

# The board's linker fragment puts main's static data in PSRAM (linker.lf); the emulator has no PSRAM.
set(ldfragments)
if(NOT IDF_TARGET STREQUAL "linux")
    set(ldfragments linker.lf)
endif()

# mbedtls (ESP-IDF's own) gives the app core SHA-256 and base64 for the keys' fingerprints (tasks 6.32, 8.2).
idf_component_register(SRCS ${srcs}
                       INCLUDE_DIRS ${dirs}
                       PRIV_REQUIRES json esp_system mbedtls
                       LDFRAGMENTS ${ldfragments})

if(NOT CMAKE_BUILD_EARLY_EXPANSION)
    idf_build_get_property(project_ver PROJECT_VER)
    if(CONFIG_PAGER_TEST_HOOKS)
        # A test build says so wherever it reports its version (hello, prov_hello, device
        # info, the boot line), so a board left with press injection is plain to see.
        set(project_ver "${project_ver}+test")
    endif()
    target_compile_definitions(${COMPONENT_LIB} PRIVATE PAGER_FW_VERSION="${project_ver}")

    idf_build_get_property(target IDF_TARGET)
    if(target STREQUAL "linux")
        # The emulator: net/ssh_link links the Mac's libssh 0.12.2 (Homebrew) through pkg-config
        # (design D4, R2.2). On esp32s3 it's david-cermak/libssh, from idf_component.yml.
        find_package(PkgConfig REQUIRED)
        pkg_check_modules(LIBSSH REQUIRED IMPORTED_TARGET libssh)
        target_link_libraries(${COMPONENT_LIB} PRIVATE PkgConfig::LIBSSH)
    endif()
endif()
