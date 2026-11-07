# herdr-pager firmware: an lcd349-kit product. The same sources build for the board and the emulator:
#   idf.py -C firmware set-target esp32s3 build flash monitor     (the board; lane board)
#   lcd349 build -C firmware                                      (the emulator, in build-emu/)
# Host tests for the app core are a separate project: firmware/test_host/run.sh.
cmake_minimum_required(VERSION 3.16)
set(COMPONENTS main)
# The firmware's version, sent in hello and prov_hello (`fw`: 0-9 A-Z a-z . + -), from the one place
# the bridge's comes from too: bridge/internal/version/VERSION (phase 5 task 1.3). A build that isn't a
# published version says <number>-dev; the release passes -DPAGER_VERSION=<number>, from that file.
set(pager_version_file "${CMAKE_CURRENT_LIST_DIR}/../bridge/internal/version/VERSION")
if(NOT EXISTS "${pager_version_file}")
    message(FATAL_ERROR "no ${pager_version_file}: the firmware takes its version from the bridge's")
endif()
file(STRINGS "${pager_version_file}" pager_number LIMIT_COUNT 1)
set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS "${pager_version_file}")
if(PAGER_VERSION)
    set(PROJECT_VER "${PAGER_VERSION}")
else()
    set(PROJECT_VER "${pager_number}-dev")
endif()
if(NOT PROJECT_VER MATCHES "^[0-9A-Za-z.+-]+$")
    message(FATAL_ERROR "the firmware's version \"${PROJECT_VER}\" holds a character hello's fw can't")
endif()
include($ENV{IDF_PATH}/tools/cmake/project.cmake)
# One component lock file per target, so board and emulator builds don't fight over it (committed).
idf_build_set_property(DEPENDENCIES_LOCK dependencies.lock.${IDF_TARGET})
project(herdr-pager)
