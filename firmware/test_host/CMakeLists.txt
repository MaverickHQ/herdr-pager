# The firmware's host tests: the app core (main/app) under Unity, on ESP-IDF's linux target
# (phase 2 design D2-2). No kit and no LVGL. Run them with firmware/test_host/run.sh.
cmake_minimum_required(VERSION 3.16)
set(COMPONENTS main)
include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(pager_host_test)
