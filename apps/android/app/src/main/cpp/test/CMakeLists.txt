cmake_minimum_required(VERSION 3.22.1)

# Host-only tests for the pure parts of the usb_direct driver. This project is
# deliberately separate from ../CMakeLists.txt so the Gradle/NDK build never
# sees it.
project(usb_direct_host_tests CXX)

option(USB_DIRECT_TEST_SANITIZE "Build the host tests with the address sanitiser" OFF)

add_executable(usb_direct_host_tests
    ../usb_direct/format_choice.cpp
    ../usb_direct/packet_sizing.cpp
    ../usb_direct/ring_buffer.cpp
    ../usb_direct/uac_descriptors.cpp
    descriptor_test.cpp
    format_choice_test.cpp
    main.cpp
    packet_sizing_test.cpp
    ring_buffer_test.cpp
)

set_target_properties(usb_direct_host_tests PROPERTIES
    CXX_STANDARD 17
    CXX_STANDARD_REQUIRED ON
    CXX_EXTENSIONS OFF
)

target_include_directories(usb_direct_host_tests PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}
    ${CMAKE_CURRENT_SOURCE_DIR}/../usb_direct
)

target_compile_options(usb_direct_host_tests PRIVATE -Wall -Wextra -Werror)

if(USB_DIRECT_TEST_SANITIZE)
    target_compile_options(usb_direct_host_tests PRIVATE -fsanitize=address,undefined -fno-omit-frame-pointer)
    target_link_options(usb_direct_host_tests PRIVATE -fsanitize=address,undefined)
endif()
