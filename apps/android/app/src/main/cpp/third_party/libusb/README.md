# Vendored libusb

Upstream: <https://github.com/libusb/libusb>

| Field | Value |
| --- | --- |
| Version | 1.0.27 |
| Tag | `v1.0.27` |
| Release tarball | <https://github.com/libusb/libusb/releases/download/v1.0.27/libusb-1.0.27.tar.bz2> |
| Tarball SHA-256 | `ffaa41d741a8a3bee244ac8e54a72ea05bf2879663c098c82fc5757853441575` |
| License | LGPL-2.1-or-later (`COPYING`) |

Only the files the Android `linux_usbfs` backend needs are vendored, copied verbatim
from that tarball:

- `libusb/core.c`, `descriptor.c`, `hotplug.c`, `io.c`, `sync.c`, `strerror.c`
- `libusb/libusb.h`, `libusbi.h`, `version.h`, `version_nano.h`
- `libusb/os/linux_usbfs.{c,h}`, `linux_netlink.c`, `events_posix.{c,h}`, `threads_posix.{c,h}`
- `android/config.h` (the upstream Android build configuration)

`COPYING` is the upstream LGPL-2.1 text. Nothing here is modified; the file list matches
`android/jni/libusb.mk` from the same tarball.

## Relicensing boundary

libusb stays LGPL-2.1-or-later and is built as its own shared library (`libusb1.0.so`)
by `apps/android/app/src/main/cpp/CMakeLists.txt`, dynamically linked by the Apache-2.0
`libusb_direct.so`. The notice in `apps/android/app/src/main/assets/THIRD-PARTY-NOTICES.txt`
records that boundary and where to obtain the corresponding source.

## Refreshing the copy

```sh
curl -sSLO https://github.com/libusb/libusb/releases/download/v1.0.27/libusb-1.0.27.tar.bz2
echo 'ffaa41d741a8a3bee244ac8e54a72ea05bf2879663c098c82fc5757853441575  libusb-1.0.27.tar.bz2' | sha256sum -c -
tar xjf libusb-1.0.27.tar.bz2
```

Then copy the files listed above and update this file's version, URL and checksum.
