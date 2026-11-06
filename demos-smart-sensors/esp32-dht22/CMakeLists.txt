# The smart sensor demo: a DHT22 climate sensor on an ESP32/ESP32-S3 as an
# OEC smart device with the OEC Micro SDK (README). Build with build.sh.
cmake_minimum_required(VERSION 3.22)
include($ENV{IDF_PATH}/tools/cmake/project.cmake)
idf_build_set_property(MINIMAL_BUILD ON)
set(PROJECT_VER "0.1.0")
project(oec_smart_sensor_dht22)
