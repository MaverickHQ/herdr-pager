#!/bin/sh
# The firmware's host tests (design D2-2): builds firmware/test_host for ESP-IDF's linux target,
# then runs every registered test. Exits non-zero if the build or any test fails; a failed build
# prints its log.
#   firmware/test_host/run.sh
here=$(cd "$(dirname "$0")" && pwd) || exit 1
if ! command -v idf.py >/dev/null 2>&1; then
    . "${IDF_PATH:-$HOME/esp/v5.5.6/esp-idf}/export.sh" >/dev/null || exit 1
fi
unset IDF_TARGET # sdkconfig.defaults sets linux
build="$here/build"
mkdir -p "$build" || exit 1
if ! idf.py --preview -C "$here" -B "$build" -D SDKCONFIG="$build/sdkconfig" build >"$build/run-build.log" 2>&1; then
    cat "$build/run-build.log"
    echo "run.sh: the host tests didn't build (log: $build/run-build.log)" >&2
    exit 1
fi
exec "$build/pager_host_test.elf"
