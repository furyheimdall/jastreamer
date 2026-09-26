// Copyright 2026 The Jastreamer Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// libusb layer: drives a USB Audio Class DAC over isochronous OUT transfers
// from a file descriptor handed to us by the Android USB host API.

#ifndef USB_DIRECT_USB_DEVICE_H_
#define USB_DIRECT_USB_DEVICE_H_

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <map>
#include <memory>
#include <mutex>
#include <string>
#include <thread>
#include <vector>

#include "libusb.h"
#include "packet_sizing.h"
#include "ring_buffer.h"
#include "uac_descriptors.h"

namespace usb_direct {

struct IsoTransfer;

class UsbDirectDevice {
 public:
  // Wraps an Android USB file descriptor. The caller keeps ownership of `fd`.
  // Returns nullptr and fills `error` on failure.
  static std::unique_ptr<UsbDirectDevice> OpenFromFd(int fd,
                                                     std::string* error);

  UsbDirectDevice(const UsbDirectDevice&) = delete;
  UsbDirectDevice& operator=(const UsbDirectDevice&) = delete;
  ~UsbDirectDevice();

  // Capabilities JSON for the Kotlin layer. Empty string on failure.
  std::string Capabilities(std::string* error) const;

  // Configures the device and starts streaming. Returns the start JSON, or an
  // empty string with `error` filled in.
  std::string Start(uint32_t sample_rate, uint32_t channels, uint32_t bits,
                    std::string* error);

  // Queues PCM bytes for the isochronous feeder. Returns the number of bytes
  // accepted, or -1 with `error` filled in.
  int Write(const uint8_t* data, size_t length, std::string* error);

  // Runtime counters as JSON. Always valid.
  std::string Status() const;

  // Stops streaming and parks the device on its zero-bandwidth alt setting.
  void Stop();

  // Stops, releases interfaces and closes the handle. Idempotent.
  void Close();

 private:
  UsbDirectDevice(libusb_context* context, libusb_device_handle* handle);

  // Descriptor and clock discovery, run once from OpenFromFd.
  bool Initialise(std::string* error);
  bool ReadConfiguration(std::string* error);
  void ReadProduct();
  bool ClaimInterface(uint8_t interface_number, std::string* error);
  void ReleaseInterfaces();
  std::vector<uint32_t> QueryUac2Rates(uint8_t clock_id) const;
  const std::vector<uint32_t>& RatesForAlt(const AltSetting& alt) const;

  const AltSetting* SelectAlt(uint32_t sample_rate, uint32_t channels,
                              uint32_t bits, std::string* error) const;
  bool ApplySampleRate(const AltSetting& alt, uint32_t sample_rate,
                       uint32_t* actual_rate, std::string* error);
  bool SubmitTransfers(std::string* error);
  void StopLocked();
  void EventLoop();

  void OnDataComplete(IsoTransfer* iso);
  void OnFeedbackComplete(IsoTransfer* iso);
  void FillDataTransfer(IsoTransfer* iso);
  static void LIBUSB_CALL DataCallback(libusb_transfer* transfer);
  static void LIBUSB_CALL FeedbackCallback(libusb_transfer* transfer);

  void SetStreamError(const std::string& message);

  libusb_context* context_ = nullptr;
  libusb_device_handle* handle_ = nullptr;
  bool closed_ = false;

  UacConfig config_;
  std::string product_;
  bool high_speed_ = false;
  std::vector<uint8_t> claimed_interfaces_;
  std::vector<uint8_t> detached_interfaces_;
  std::map<uint8_t, std::vector<uint32_t>> uac2_rates_;
  std::vector<uint32_t> empty_rates_;

  mutable std::mutex state_mutex_;

  // Stream configuration, written under `state_mutex_` before any transfer is
  // submitted and only read afterwards.
  AltSetting active_alt_;
  bool streaming_configured_ = false;
  uint8_t streaming_interface_ = 0;
  uint32_t frame_bytes_ = 0;
  uint32_t max_frames_per_packet_ = 0;
  uint32_t packets_per_transfer_ = 0;
  uint32_t nominal_unit_q16_ = 0;     // Per (micro)frame.
  uint32_t nominal_service_q16_ = 0;  // Per service interval.
  uint32_t feedback_packet_bytes_ = 0;
  uint32_t ring_bytes_ = 0;
  uint32_t actual_sample_rate_ = 0;

  std::shared_ptr<RingBuffer> ring_;
  mutable std::mutex ring_mutex_;
  RingBuffer* ring_raw_ = nullptr;  // Valid while transfers are in flight.
  PacketSizer sizer_;               // Event thread only.

  std::vector<std::unique_ptr<IsoTransfer>> transfers_;
  std::thread event_thread_;
  std::atomic<bool> event_exit_{false};
  std::atomic<bool> running_{false};
  std::atomic<bool> stopping_{false};
  std::atomic<int> active_transfers_{0};
  std::mutex done_mutex_;
  std::condition_variable done_cv_;

  std::atomic<uint64_t> packets_{0};
  std::atomic<uint64_t> underruns_{0};
  std::atomic<uint64_t> frames_{0};
  std::atomic<uint32_t> feedback_q16_{0};
  // Set once the ring has delivered audio, so priming silence is not counted
  // as an underrun.
  std::atomic<bool> flowing_{false};

  mutable std::mutex error_mutex_;
  std::string stream_error_;
};

}  // namespace usb_direct

#endif  // USB_DIRECT_USB_DEVICE_H_
