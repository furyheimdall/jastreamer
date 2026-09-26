cmake_minimum_required(VERSION 3.25)
project(jastreamer_native_audio VERSION 0.2.1 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
set(CMAKE_POSITION_INDEPENDENT_CODE ON)
include(CTest)

set(JASTREAMER_FFMPEG_ROOT "" CACHE PATH "Prefix containing source-built FFmpeg headers and libraries")
set(JASTREAMER_NLOHMANN_ROOT "" CACHE PATH "Extracted nlohmann/json source tree")

if(MSVC)
  set(CMAKE_MSVC_RUNTIME_LIBRARY "MultiThreaded$<$<CONFIG:Debug>:Debug>")
  add_link_options(/Brepro)
endif()

set(JASTREAMER_FFMPEG_TARGETS "")
if(WIN32)
  if(NOT JASTREAMER_FFMPEG_ROOT)
    message(FATAL_ERROR "JASTREAMER_FFMPEG_ROOT is required on Windows; use scripts/build-native.ps1")
  endif()
  foreach(component IN ITEMS avformat avcodec avutil swresample)
    # FFmpeg's MSVC shared build installs import libraries beside its DLLs.
    find_library(FFMPEG_${component}_LIBRARY
      NAMES ${component}
      PATHS "${JASTREAMER_FFMPEG_ROOT}/bin"
      NO_DEFAULT_PATH REQUIRED)
    if(component STREQUAL "avformat")
      set(dll_name "avformat-62.dll")
    elseif(component STREQUAL "avcodec")
      set(dll_name "avcodec-62.dll")
    elseif(component STREQUAL "avutil")
      set(dll_name "avutil-60.dll")
    else()
      set(dll_name "swresample-6.dll")
    endif()
    find_file(FFMPEG_${component}_DLL
      NAMES "${dll_name}"
      PATHS "${JASTREAMER_FFMPEG_ROOT}/bin"
      NO_DEFAULT_PATH REQUIRED)
    add_library(FFmpeg::${component} SHARED IMPORTED)
    set_target_properties(FFmpeg::${component} PROPERTIES
      IMPORTED_IMPLIB "${FFMPEG_${component}_LIBRARY}"
      IMPORTED_LOCATION "${FFMPEG_${component}_DLL}"
      INTERFACE_INCLUDE_DIRECTORIES "${JASTREAMER_FFMPEG_ROOT}/include")
    list(APPEND JASTREAMER_FFMPEG_TARGETS FFmpeg::${component})
  endforeach()
elseif(JASTREAMER_FFMPEG_ROOT)
  foreach(component IN ITEMS avformat avcodec avutil swresample)
    find_library(FFMPEG_${component}_LIBRARY
      NAMES ${component}
      PATHS "${JASTREAMER_FFMPEG_ROOT}/lib"
      NO_DEFAULT_PATH REQUIRED)
    add_library(FFmpeg::${component} UNKNOWN IMPORTED)
    set_target_properties(FFmpeg::${component} PROPERTIES
      IMPORTED_LOCATION "${FFMPEG_${component}_LIBRARY}"
      INTERFACE_INCLUDE_DIRECTORIES "${JASTREAMER_FFMPEG_ROOT}/include")
    list(APPEND JASTREAMER_FFMPEG_TARGETS FFmpeg::${component})
  endforeach()
  set(CMAKE_BUILD_RPATH "${JASTREAMER_FFMPEG_ROOT}/lib")
else()
  find_package(PkgConfig REQUIRED)
  pkg_check_modules(FFMPEG REQUIRED IMPORTED_TARGET libavformat libavcodec libavutil libswresample)
  list(APPEND JASTREAMER_FFMPEG_TARGETS PkgConfig::FFMPEG)
endif()

add_library(jastreamer_decoder STATIC decoder.cpp)
target_include_directories(jastreamer_decoder PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_link_libraries(jastreamer_decoder PUBLIC ${JASTREAMER_FFMPEG_TARGETS})

add_executable(jastreamer-decoder-smoke decoder_smoke.cpp)
target_link_libraries(jastreamer-decoder-smoke PRIVATE jastreamer_decoder)

add_executable(jastreamer-decoder-behavior decoder_behavior.cpp)
target_link_libraries(jastreamer-decoder-behavior PRIVATE jastreamer_decoder)
if(BUILD_TESTING)
  add_test(NAME decoder COMMAND jastreamer-decoder-behavior)
endif()


if(WIN32 AND NOT MSVC)
  message(FATAL_ERROR "The Windows native helper requires the MSVC C++20 toolchain")
endif()
if(NOT JASTREAMER_NLOHMANN_ROOT OR NOT EXISTS "${JASTREAMER_NLOHMANN_ROOT}/CMakeLists.txt")
  message(FATAL_ERROR "JASTREAMER_NLOHMANN_ROOT must point to the verified nlohmann/json source tree")
endif()
set(JSON_BuildTests OFF CACHE BOOL "" FORCE)
set(JSON_Install OFF CACHE BOOL "" FORCE)
add_subdirectory("${JASTREAMER_NLOHMANN_ROOT}" "${CMAKE_CURRENT_BINARY_DIR}/nlohmann" EXCLUDE_FROM_ALL)

add_library(jastreamer_native STATIC
  audio_engine.cpp
  protocol.cpp)
target_include_directories(jastreamer_native PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_link_libraries(jastreamer_native PUBLIC jastreamer_decoder nlohmann_json::nlohmann_json)
if(WIN32)
target_sources(jastreamer_native PRIVATE smtc.cpp)
target_compile_definitions(jastreamer_native PUBLIC
  NOMINMAX
  WIN32_LEAN_AND_MEAN
  UNICODE
  WINVER=0x0A00
  _UNICODE
  _WIN32_WINNT=0x0A00)
target_compile_options(jastreamer_native PRIVATE /W4 /permissive- /EHsc /utf-8)
target_link_libraries(jastreamer_native PUBLIC
  avrt
  ksuser
  ole32
  propsys
  shcore
  shlwapi
  user32
  uuid
  windowsapp)

add_executable(jastreamer-audio main.cpp)
target_compile_options(jastreamer-audio PRIVATE /W4 /permissive- /EHsc /utf-8)
target_link_libraries(jastreamer-audio PRIVATE jastreamer_native)
set(native_dist "${CMAKE_CURRENT_SOURCE_DIR}/dist")
set_target_properties(jastreamer-audio PROPERTIES
  RUNTIME_OUTPUT_DIRECTORY "${native_dist}"
  RUNTIME_OUTPUT_DIRECTORY_DEBUG "${native_dist}"
  RUNTIME_OUTPUT_DIRECTORY_RELEASE "${native_dist}"
  RUNTIME_OUTPUT_DIRECTORY_RELWITHDEBINFO "${native_dist}"
  RUNTIME_OUTPUT_DIRECTORY_MINSIZEREL "${native_dist}")
endif()

add_executable(jastreamer-packing-test tests/packing_test.cpp)
target_link_libraries(jastreamer-packing-test PRIVATE jastreamer_native)
add_executable(jastreamer-protocol-smoke tests/protocol_smoke.cpp)
target_link_libraries(jastreamer-protocol-smoke PRIVATE jastreamer_native)
if(WIN32)
function(stage_ffmpeg_runtime target)
  foreach(component IN ITEMS avformat avcodec avutil swresample)
    add_custom_command(TARGET ${target} POST_BUILD
      COMMAND "${CMAKE_COMMAND}" -E copy_if_different
        "$<TARGET_FILE:FFmpeg::${component}>"
        "$<TARGET_FILE_DIR:${target}>"
      VERBATIM)
  endforeach()
endfunction()
foreach(target IN ITEMS
    jastreamer-audio
    jastreamer-decoder-smoke
    jastreamer-decoder-behavior
    jastreamer-packing-test
    jastreamer-protocol-smoke)
  stage_ffmpeg_runtime(${target})
endforeach()
endif()
if(BUILD_TESTING)
  add_test(NAME packing COMMAND jastreamer-packing-test)
  add_test(NAME protocol COMMAND jastreamer-protocol-smoke)
endif()
if(NOT WIN32)
  message(STATUS "Building decoder and portable PCM/protocol checks; WASAPI and SMTC are unavailable")
endif()
