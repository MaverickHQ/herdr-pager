# The runner, the app core's sources, and every test file, taken by directory:
#   firmware/test_host/test_*.c     one per area (test_model.c, test_router.c ...)
#   firmware/main/app/*_test.c      tests kept beside an area's code (built only here)
# Each test file registers itself with TEST_AREA (test_registry.h), so no build list changes.
set(fw "${CMAKE_CURRENT_LIST_DIR}/../../main")
# CONFIGURE_DEPENDS makes the build notice a new file; it's invalid in ESP-IDF's script-mode pass.
set(glob_opts)
if(NOT CMAKE_BUILD_EARLY_EXPANSION)
    set(glob_opts CONFIGURE_DEPENDS)
endif()
file(GLOB app_srcs ${glob_opts} "${fw}/app/*.c")
list(FILTER app_srcs EXCLUDE REGEX "_test\\.c$")
file(GLOB test_srcs ${glob_opts} "${CMAKE_CURRENT_LIST_DIR}/../test_*.c" "${fw}/app/*_test.c")

# The app core's requirements, as in firmware/main: json (the codec) and mbedtls (the fingerprints).

idf_component_register(SRCS "test_main.c" ${app_srcs} ${test_srcs}
                       INCLUDE_DIRS "." "${fw}/app"
                       PRIV_REQUIRES unity json mbedtls
                       WHOLE_ARCHIVE)

if(NOT CMAKE_BUILD_EARLY_EXPANSION)
    # The fixtures both sides read (bridge/testdata/protocol and provisioning, design D2-6, D2-13).
    get_filename_component(testdata "${CMAKE_CURRENT_LIST_DIR}/../../../bridge/testdata" ABSOLUTE)
    target_compile_definitions(${COMPONENT_LIB} PRIVATE PAGER_TESTDATA="${testdata}")
endif()
