cmake_minimum_required(VERSION 3.22.1)

project(usb_direct C CXX)

# Vendored libusb 1.0.27, built for Android with device discovery disabled: the
# app always wraps a file descriptor handed over by the Android USB host API.
add_library(usb1.0 SHARED
    third_party/libusb/libusb/core.c
    third_party/libusb/libusb/descriptor.c
    third_party/libusb/libusb/hotplug.c
    third_party/libusb/libusb/io.c
    third_party/libusb/libusb/strerror.c
    third_party/libusb/libusb/sync.c
    third_party/libusb/libusb/os/linux_usbfs.c
    third_party/libusb/libusb/os/linux_netlink.c
    third_party/libusb/libusb/os/events_posix.c
    third_party/libusb/libusb/os/threads_posix.c
)

target_include_directories(usb1.0
    PRIVATE
        third_party/libusb/android
        third_party/libusb/libusb/os
    PUBLIC
        third_party/libusb/libusb
)

# Upstream code is not warning clean under our flags; silence exactly what it
# needs so our own sources keep full warnings.
target_compile_options(usb1.0 PRIVATE
    -fvisibility=hidden
    -Wno-unused-parameter
    -Wno-sign-compare
)

target_link_libraries(usb1.0 PRIVATE log)

set_target_properties(usb1.0 PROPERTIES PREFIX "lib" OUTPUT_NAME "usb1.0")

# Our USB Audio Class driver and its JNI surface.
add_library(usb_direct SHARED
    usb_direct/format_choice.cpp
    usb_direct/jni_bridge.cpp
    usb_direct/json.cpp
    usb_direct/packet_sizing.cpp
    usb_direct/ring_buffer.cpp
    usb_direct/sample_pack.cpp
    usb_direct/uac_descriptors.cpp
    usb_direct/usb_device.cpp
)

set_target_properties(usb_direct PROPERTIES
    CXX_STANDARD 17
    CXX_STANDARD_REQUIRED ON
    CXX_EXTENSIONS OFF
)

target_compile_options(usb_direct PRIVATE -Wall -Wextra -Werror)

target_link_libraries(usb_direct PRIVATE usb1.0 log android)
