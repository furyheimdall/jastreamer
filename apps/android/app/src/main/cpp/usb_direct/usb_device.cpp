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

#include "usb_device.h"

#include <cstdarg>
#include <cstdio>
#include <cstring>

#include "format_choice.h"
#include "json.h"

#if defined(__ANDROID__)
#include <android/log.h>
#endif

namespace usb_direct {
namespace {

constexpr unsigned int kControlTimeoutMs = 1000;
constexpr unsigned int kIsoTimeoutMs = 1000;
constexpr uint32_t kDataTransferCount = 8;
constexpr uint32_t kFeedbackTransferCount = 2;
constexpr uint32_t kHighSpeedPacketsPerTransfer = 8;
constexpr uint32_t kFullSpeedPacketsPerTransfer = 1;
constexpr uint32_t kFeedbackTolerancePercent = 20;
constexpr uint32_t kRingMilliseconds = 200;
constexpr size_t kMaxUac2RatesPerFormat = 64;
constexpr int kStopWaitMs = 2000;

// UAC request codes and selectors.
constexpr uint8_t kRequestSetCur = 0x01;
constexpr uint8_t kRequestGetCur = 0x81;   // UAC1 GET_CUR.
constexpr uint8_t kRequestUac2Cur = 0x01;  // UAC2 CUR (direction in bmRequestType).
constexpr uint8_t kRequestUac2Range = 0x02;
constexpr uint16_t kSelectorSamplingFreq = 0x0100;

void LogLine(const char* format, ...) __attribute__((format(printf, 1, 2)));

void LogLine(const char* format, ...) {
  va_list args;
  va_start(args, format);
#if defined(__ANDROID__)
  __android_log_vprint(ANDROID_LOG_INFO, "UsbDirectNative", format, args);
#else
  std::vfprintf(stderr, format, args);
  std::fputc('\n', stderr);
#endif
  va_end(args);
}

std::string Format(const char* format, ...) __attribute__((format(printf, 1, 2)));

std::string Format(const char* format, ...) {
  char buffer[512];
  va_list args;
  va_start(args, format);
  const int written = std::vsnprintf(buffer, sizeof(buffer), format, args);
  va_end(args);
  if (written <= 0) {
    return std::string();
  }
  const size_t length = static_cast<size_t>(written) < sizeof(buffer)
                            ? static_cast<size_t>(written)
                            : sizeof(buffer) - 1;
  return std::string(buffer, length);
}

std::string UsbError(const char* what, int code) {
  return Format("%s failed: %s (%d)", what, libusb_strerror(
                                                static_cast<libusb_error>(code)),
                code);
}

void SetError(std::string* error, const std::string& message) {
  if (error != nullptr) {
    *error = message;
  }
}

const char* SyncName(SyncType sync) {
  switch (sync) {
    case SyncType::kAsync:
      return "async";
    case SyncType::kAdaptive:
      return "adaptive";
    case SyncType::kSync:
      return "sync";
    case SyncType::kNone:
    default:
      return "none";
  }
}

std::once_flag g_init_once;
libusb_context* g_context = nullptr;
int g_init_result = LIBUSB_ERROR_OTHER;

void InitialiseLibusb() {
  const int option_rc =
      libusb_set_option(nullptr, LIBUSB_OPTION_NO_DEVICE_DISCOVERY);
  if (option_rc != LIBUSB_SUCCESS) {
    LogLine("libusb_set_option(NO_DEVICE_DISCOVERY) returned %d", option_rc);
  }
  g_init_result = libusb_init(&g_context);
  if (g_init_result != LIBUSB_SUCCESS) {
    g_context = nullptr;
  }
}

}  // namespace

// One isochronous transfer plus its backing buffer.
struct IsoTransfer {
  libusb_transfer* transfer = nullptr;
  std::vector<uint8_t> buffer;
  UsbDirectDevice* device = nullptr;
  bool feedback = false;
  std::atomic<bool> in_flight{false};
};

UsbDirectDevice::UsbDirectDevice(libusb_context* context,
                                 libusb_device_handle* handle)
    : context_(context), handle_(handle) {}

UsbDirectDevice::~UsbDirectDevice() { Close(); }

std::unique_ptr<UsbDirectDevice> UsbDirectDevice::OpenFromFd(
    int fd, std::string* error) {
  if (fd < 0) {
    SetError(error, "invalid USB file descriptor");
    return nullptr;
  }
  std::call_once(g_init_once, InitialiseLibusb);
  if (g_context == nullptr) {
    SetError(error, UsbError("libusb_init", g_init_result));
    return nullptr;
  }

  libusb_device_handle* handle = nullptr;
  const int rc = libusb_wrap_sys_device(
      g_context, static_cast<intptr_t>(fd), &handle);
  if (rc != LIBUSB_SUCCESS || handle == nullptr) {
    SetError(error, UsbError("libusb_wrap_sys_device", rc));
    return nullptr;
  }

  std::unique_ptr<UsbDirectDevice> device(
      new UsbDirectDevice(g_context, handle));
  if (!device->Initialise(error)) {
    return nullptr;
  }
  return device;
}

bool UsbDirectDevice::Initialise(std::string* error) {
  libusb_device* device = libusb_get_device(handle_);
  if (device == nullptr) {
    SetError(error, "libusb_get_device returned no device");
    return false;
  }
  const int speed = libusb_get_device_speed(device);
  high_speed_ = speed >= LIBUSB_SPEED_HIGH;

  if (!ReadConfiguration(error)) {
    return false;
  }
  ReadProduct();

  if (config_.version == UacVersion::kUac2) {
    std::string claim_error;
    if (!ClaimInterface(config_.control_interface, &claim_error)) {
      SetError(error, "cannot claim the audio control interface: " +
                          claim_error);
      return false;
    }
    for (const AltSetting& alt : config_.alts) {
      if (alt.clock_source_id == 0 ||
          uac2_rates_.find(alt.clock_source_id) != uac2_rates_.end()) {
        continue;
      }
      uac2_rates_[alt.clock_source_id] = QueryUac2Rates(alt.clock_source_id);
    }
  }
  return true;
}

bool UsbDirectDevice::ReadConfiguration(std::string* error) {
  uint8_t header[9] = {0};
  int rc = libusb_get_descriptor(handle_, LIBUSB_DT_CONFIG, 0, header,
                                 static_cast<int>(sizeof(header)));
  if (rc < static_cast<int>(sizeof(header))) {
    SetError(error, UsbError("reading the configuration descriptor header",
                             rc < 0 ? rc : LIBUSB_ERROR_IO));
    return false;
  }
  const uint16_t total_length =
      static_cast<uint16_t>(header[2] | (static_cast<uint16_t>(header[3]) << 8));
  if (total_length < sizeof(header)) {
    SetError(error, Format("configuration descriptor reports %u bytes",
                           static_cast<unsigned>(total_length)));
    return false;
  }
  std::vector<uint8_t> block(total_length, 0);
  rc = libusb_get_descriptor(handle_, LIBUSB_DT_CONFIG, 0, block.data(),
                             static_cast<int>(block.size()));
  if (rc < 0) {
    SetError(error, UsbError("reading the configuration descriptor", rc));
    return false;
  }
  block.resize(static_cast<size_t>(rc));

  config_ = ParseConfiguration(block.data(), block.size());
  if (config_.alts.empty()) {
    SetError(error, config_.error.empty()
                        ? std::string("no USB audio output interface found")
                        : config_.error);
    return false;
  }
  return true;
}

void UsbDirectDevice::ReadProduct() {
  libusb_device* device = libusb_get_device(handle_);
  libusb_device_descriptor descriptor;
  std::memset(&descriptor, 0, sizeof(descriptor));
  if (device == nullptr ||
      libusb_get_device_descriptor(device, &descriptor) != LIBUSB_SUCCESS) {
    return;
  }
  if (descriptor.iProduct == 0) {
    return;
  }
  unsigned char text[256] = {0};
  const int rc = libusb_get_string_descriptor_ascii(
      handle_, descriptor.iProduct, text, static_cast<int>(sizeof(text) - 1));
  if (rc > 0) {
    product_.assign(reinterpret_cast<const char*>(text),
                    static_cast<size_t>(rc));
  }
}

bool UsbDirectDevice::ClaimInterface(uint8_t interface_number,
                                     std::string* error) {
  for (const uint8_t claimed : claimed_interfaces_) {
    if (claimed == interface_number) {
      return true;
    }
  }
  const int detach_rc = libusb_set_auto_detach_kernel_driver(handle_, 1);
  if (detach_rc != LIBUSB_SUCCESS && detach_rc != LIBUSB_ERROR_NOT_SUPPORTED) {
    LogLine("libusb_set_auto_detach_kernel_driver returned %d", detach_rc);
  }
  const bool had_kernel_driver =
      libusb_kernel_driver_active(handle_, interface_number) == 1;
  const int rc = libusb_claim_interface(handle_, interface_number);
  if (rc != LIBUSB_SUCCESS) {
    SetError(error, UsbError(Format("claiming interface %u",
                                    static_cast<unsigned>(interface_number))
                                 .c_str(),
                             rc));
    return false;
  }
  claimed_interfaces_.push_back(interface_number);
  if (had_kernel_driver) {
    detached_interfaces_.push_back(interface_number);
  }
  return true;
}

void UsbDirectDevice::ReleaseInterfaces() {
  for (const uint8_t interface_number : claimed_interfaces_) {
    const int rc = libusb_release_interface(handle_, interface_number);
    if (rc != LIBUSB_SUCCESS && rc != LIBUSB_ERROR_NO_DEVICE) {
      LogLine("libusb_release_interface(%u) returned %d",
              static_cast<unsigned>(interface_number), rc);
    }
  }
  for (const uint8_t interface_number : detached_interfaces_) {
    const int rc = libusb_attach_kernel_driver(handle_, interface_number);
    if (rc != LIBUSB_SUCCESS && rc != LIBUSB_ERROR_NOT_FOUND &&
        rc != LIBUSB_ERROR_NO_DEVICE && rc != LIBUSB_ERROR_NOT_SUPPORTED) {
      LogLine("libusb_attach_kernel_driver(%u) returned %d",
              static_cast<unsigned>(interface_number), rc);
    }
  }
  claimed_interfaces_.clear();
  detached_interfaces_.clear();
}

std::vector<uint32_t> UsbDirectDevice::QueryUac2Rates(uint8_t clock_id) const {
  std::vector<uint32_t> rates;
  uint8_t reply[1024] = {0};
  const uint16_t index = static_cast<uint16_t>(
      (static_cast<uint16_t>(clock_id) << 8) | config_.control_interface);
  const int rc = libusb_control_transfer(
      handle_, 0xA1, kRequestUac2Range, kSelectorSamplingFreq, index, reply,
      static_cast<uint16_t>(sizeof(reply)), kControlTimeoutMs);
  if (rc < 2) {
    LogLine("clock %u sample-rate RANGE query failed (%d)",
            static_cast<unsigned>(clock_id), rc);
    return rates;
  }
  const size_t length = static_cast<size_t>(rc);
  const size_t sub_ranges =
      static_cast<size_t>(reply[0]) | (static_cast<size_t>(reply[1]) << 8);
  for (size_t i = 0; i < sub_ranges; ++i) {
    const size_t offset = 2 + i * 12;
    if (offset + 12 > length) {
      break;
    }
    const uint8_t* triplet = reply + offset;
    auto read32 = [](const uint8_t* p) {
      return static_cast<uint32_t>(p[0]) | (static_cast<uint32_t>(p[1]) << 8) |
             (static_cast<uint32_t>(p[2]) << 16) |
             (static_cast<uint32_t>(p[3]) << 24);
    };
    const uint32_t min_rate = read32(triplet);
    const uint32_t max_rate = read32(triplet + 4);
    const uint32_t resolution = read32(triplet + 8);
    if (min_rate == 0) {
      continue;
    }
    rates.push_back(min_rate);
    if (resolution > 0 && max_rate > min_rate) {
      size_t expanded = 0;
      for (uint32_t rate = min_rate + resolution;
           rate < max_rate && expanded < kMaxUac2RatesPerFormat;
           rate += resolution, ++expanded) {
        rates.push_back(rate);
      }
    }
    if (max_rate > min_rate) {
      rates.push_back(max_rate);
    }
  }
  return rates;
}

const std::vector<uint32_t>& UsbDirectDevice::RatesForAlt(
    const AltSetting& alt) const {
  if (config_.version == UacVersion::kUac2) {
    const auto it = uac2_rates_.find(alt.clock_source_id);
    if (it != uac2_rates_.end()) {
      return it->second;
    }
    return empty_rates_;
  }
  return alt.rates;
}

std::string UsbDirectDevice::Capabilities(std::string* error) const {
  std::lock_guard<std::mutex> lock(state_mutex_);
  if (handle_ == nullptr || closed_) {
    SetError(error, "device is closed");
    return std::string();
  }
  JsonWriter json;
  json.BeginObject();
  json.Field("uac_version",
             static_cast<int64_t>(config_.version == UacVersion::kUac2 ? 2 : 1));
  json.Field("high_speed", high_speed_);
  json.Field("control_interface",
             static_cast<int64_t>(config_.control_interface));
  json.Field("streaming_interface",
             static_cast<int64_t>(config_.alts.empty()
                                      ? 0
                                      : config_.alts.front().interface_number));
  json.Field("product", product_);
  json.Key("formats");
  json.BeginArray();
  for (const AltSetting& alt : config_.alts) {
    json.BeginObject();
    json.Field("alt", static_cast<int64_t>(alt.alt_setting));
    json.Field("bits", static_cast<int64_t>(alt.bit_resolution));
    json.Field("subslot_bytes", static_cast<int64_t>(alt.subslot_bytes));
    json.Field("channels", static_cast<int64_t>(alt.channels));
    json.Key("rates");
    json.BeginArray();
    for (const uint32_t rate : RatesForAlt(alt)) {
      json.Number(static_cast<uint64_t>(rate));
    }
    if (config_.version == UacVersion::kUac1 && alt.rates.empty() &&
        alt.rate_min > 0) {
      json.Number(static_cast<uint64_t>(alt.rate_min));
      if (alt.rate_max > alt.rate_min) {
        json.Number(static_cast<uint64_t>(alt.rate_max));
      }
    }
    json.EndArray();
    json.Field("sync", SyncName(alt.sync));
    json.Field("feedback", alt.feedback_endpoint != 0);
    json.Field("max_packet_bytes", static_cast<int64_t>(alt.max_packet_bytes));
    json.Field("interval", static_cast<int64_t>(alt.interval));
    json.EndObject();
  }
  json.EndArray();
  json.EndObject();
  return json.Result();
}

const AltSetting* UsbDirectDevice::SelectAlt(uint32_t sample_rate,
                                             uint32_t channels, uint32_t bits,
                                             std::string* error) const {
  std::vector<std::vector<uint32_t>> rates;
  rates.reserve(config_.alts.size());
  for (const AltSetting& alt : config_.alts) {
    rates.push_back(RatesForAlt(alt));
  }
  const int index = ChooseAltSetting(config_.alts, rates, sample_rate, channels,
                                     bits, error);
  if (index < 0) {
    return nullptr;
  }
  return &config_.alts[static_cast<size_t>(index)];
}

bool UsbDirectDevice::ApplySampleRate(const AltSetting& alt,
                                      uint32_t sample_rate,
                                      uint32_t* actual_rate,
                                      std::string* error) {
  *actual_rate = sample_rate;
  if (config_.version == UacVersion::kUac2) {
    if (alt.clock_source_id == 0) {
      SetError(error, "device does not expose a clock source to set the rate");
      return false;
    }
    uint8_t payload[4] = {
        static_cast<uint8_t>(sample_rate & 0xFF),
        static_cast<uint8_t>((sample_rate >> 8) & 0xFF),
        static_cast<uint8_t>((sample_rate >> 16) & 0xFF),
        static_cast<uint8_t>((sample_rate >> 24) & 0xFF)};
    const uint16_t index = static_cast<uint16_t>(
        (static_cast<uint16_t>(alt.clock_source_id) << 8) |
        config_.control_interface);
    int rc = libusb_control_transfer(handle_, 0x21, kRequestUac2Cur,
                                     kSelectorSamplingFreq, index, payload,
                                     sizeof(payload), kControlTimeoutMs);
    if (rc != static_cast<int>(sizeof(payload))) {
      SetError(error, UsbError(Format("setting the sample rate to %u Hz",
                                      static_cast<unsigned>(sample_rate))
                                   .c_str(),
                               rc));
      return false;
    }
    uint8_t read_back[4] = {0};
    rc = libusb_control_transfer(handle_, 0xA1, kRequestUac2Cur,
                                 kSelectorSamplingFreq, index, read_back,
                                 sizeof(read_back), kControlTimeoutMs);
    if (rc != static_cast<int>(sizeof(read_back))) {
      LogLine("sample rate read-back failed (%d); assuming %u Hz", rc,
              static_cast<unsigned>(sample_rate));
      return true;
    }
    const uint32_t reported = static_cast<uint32_t>(read_back[0]) |
                              (static_cast<uint32_t>(read_back[1]) << 8) |
                              (static_cast<uint32_t>(read_back[2]) << 16) |
                              (static_cast<uint32_t>(read_back[3]) << 24);
    if (reported != sample_rate) {
      SetError(error, Format("device reports %u Hz after being set to %u Hz",
                             static_cast<unsigned>(reported),
                             static_cast<unsigned>(sample_rate)));
      return false;
    }
    *actual_rate = reported;
    return true;
  }

  uint8_t payload[3] = {static_cast<uint8_t>(sample_rate & 0xFF),
                        static_cast<uint8_t>((sample_rate >> 8) & 0xFF),
                        static_cast<uint8_t>((sample_rate >> 16) & 0xFF)};
  int rc = libusb_control_transfer(handle_, 0x22, kRequestSetCur,
                                   kSelectorSamplingFreq,
                                   alt.endpoint_address, payload,
                                   sizeof(payload), kControlTimeoutMs);
  if (rc != static_cast<int>(sizeof(payload))) {
    const bool single_fixed_rate =
        !alt.endpoint_rate_control && alt.rates.size() == 1 &&
        alt.rates.front() == sample_rate;
    if (!single_fixed_rate) {
      SetError(error, UsbError(Format("setting the sample rate to %u Hz",
                                      static_cast<unsigned>(sample_rate))
                                   .c_str(),
                               rc));
      return false;
    }
    LogLine("endpoint has no sampling-frequency control; device is fixed at %u Hz",
            static_cast<unsigned>(sample_rate));
    return true;
  }
  uint8_t read_back[3] = {0};
  rc = libusb_control_transfer(handle_, 0xA2, kRequestGetCur,
                               kSelectorSamplingFreq, alt.endpoint_address,
                               read_back, sizeof(read_back), kControlTimeoutMs);
  if (rc != static_cast<int>(sizeof(read_back))) {
    LogLine("sample rate read-back failed (%d); assuming %u Hz", rc,
            static_cast<unsigned>(sample_rate));
    return true;
  }
  const uint32_t reported = static_cast<uint32_t>(read_back[0]) |
                            (static_cast<uint32_t>(read_back[1]) << 8) |
                            (static_cast<uint32_t>(read_back[2]) << 16);
  if (reported != sample_rate) {
    SetError(error, Format("device reports %u Hz after being set to %u Hz",
                           static_cast<unsigned>(reported),
                           static_cast<unsigned>(sample_rate)));
    return false;
  }
  *actual_rate = reported;
  return true;
}

std::string UsbDirectDevice::Start(uint32_t sample_rate, uint32_t channels,
                                   uint32_t bits, std::string* error) {
  std::lock_guard<std::mutex> lock(state_mutex_);
  if (handle_ == nullptr || closed_) {
    SetError(error, "device is closed");
    return std::string();
  }
  if (running_.load(std::memory_order_acquire)) {
    SetError(error, "usb direct stream is already running");
    return std::string();
  }
  if (sample_rate == 0 || channels == 0 || bits == 0) {
    SetError(error, "sample rate, channel count and bit depth are required");
    return std::string();
  }

  const AltSetting* alt = SelectAlt(sample_rate, channels, bits, error);
  if (alt == nullptr) {
    return std::string();
  }
  if (!ClaimInterface(alt->interface_number, error)) {
    return std::string();
  }
  const int alt_rc = libusb_set_interface_alt_setting(
      handle_, alt->interface_number, alt->alt_setting);
  if (alt_rc != LIBUSB_SUCCESS) {
    SetError(error, UsbError(Format("selecting interface %u alt setting %u",
                                    static_cast<unsigned>(alt->interface_number),
                                    static_cast<unsigned>(alt->alt_setting))
                                 .c_str(),
                             alt_rc));
    return std::string();
  }
  streaming_interface_ = alt->interface_number;
  streaming_configured_ = true;

  uint32_t actual_rate = sample_rate;
  if (!ApplySampleRate(*alt, sample_rate, &actual_rate, error)) {
    libusb_set_interface_alt_setting(handle_, alt->interface_number, 0);
    streaming_configured_ = false;
    return std::string();
  }

  active_alt_ = *alt;
  actual_sample_rate_ = actual_rate;
  frame_bytes_ = channels * alt->subslot_bytes;
  max_frames_per_packet_ =
      frame_bytes_ == 0 ? 0 : alt->max_packet_bytes / frame_bytes_;
  if (max_frames_per_packet_ == 0) {
    SetError(error, Format("endpoint packet size %u is too small for %u-byte frames",
                           static_cast<unsigned>(alt->max_packet_bytes),
                           static_cast<unsigned>(frame_bytes_)));
    libusb_set_interface_alt_setting(handle_, alt->interface_number, 0);
    streaming_configured_ = false;
    return std::string();
  }
  packets_per_transfer_ = high_speed_ ? kHighSpeedPacketsPerTransfer
                                      : kFullSpeedPacketsPerTransfer;
  nominal_unit_q16_ = NominalQ16PerServiceInterval(sample_rate, high_speed_, 1);
  nominal_service_q16_ =
      NominalQ16PerServiceInterval(sample_rate, high_speed_, alt->interval);

  feedback_packet_bytes_ = high_speed_ ? 4u : 3u;
  if (alt->feedback_endpoint != 0) {
    const int feedback_max = libusb_get_max_packet_size(
        libusb_get_device(handle_), alt->feedback_endpoint);
    if (feedback_max > 0 && feedback_max <= 1024) {
      feedback_packet_bytes_ = static_cast<uint32_t>(feedback_max);
    }
  }

  const size_t ring_request =
      static_cast<size_t>(sample_rate) * frame_bytes_ * kRingMilliseconds / 1000;
  {
    std::lock_guard<std::mutex> ring_lock(ring_mutex_);
    ring_ = std::make_shared<RingBuffer>(ring_request);
    ring_raw_ = ring_.get();
    ring_bytes_ = static_cast<uint32_t>(ring_->Capacity());
  }

  sizer_.Reset();
  packets_.store(0, std::memory_order_relaxed);
  underruns_.store(0, std::memory_order_relaxed);
  frames_.store(0, std::memory_order_relaxed);
  feedback_q16_.store(0, std::memory_order_relaxed);
  flowing_.store(false, std::memory_order_relaxed);
  active_transfers_.store(0, std::memory_order_relaxed);
  {
    std::lock_guard<std::mutex> error_lock(error_mutex_);
    stream_error_.clear();
  }

  stopping_.store(false, std::memory_order_release);
  event_exit_.store(false, std::memory_order_release);
  running_.store(true, std::memory_order_release);
  event_thread_ = std::thread(&UsbDirectDevice::EventLoop, this);

  if (!SubmitTransfers(error)) {
    StopLocked();
    return std::string();
  }

  JsonWriter json;
  json.BeginObject();
  json.Field("sample_rate", static_cast<int64_t>(sample_rate));
  json.Field("actual_sample_rate", static_cast<int64_t>(actual_sample_rate_));
  json.Field("channels", static_cast<int64_t>(channels));
  json.Field("bits", static_cast<int64_t>(alt->bit_resolution));
  json.Field("subslot_bytes", static_cast<int64_t>(alt->subslot_bytes));
  json.Field("alt", static_cast<int64_t>(alt->alt_setting));
  json.Field("sync", SyncName(alt->sync));
  json.Field("feedback", alt->feedback_endpoint != 0);
  json.Field("packet_frames",
             static_cast<int64_t>((nominal_service_q16_ + 0xFFFFu) >> 16));
  json.Field("high_speed", high_speed_);
  json.Field("ring_bytes", static_cast<int64_t>(ring_bytes_));
  json.EndObject();
  return json.Result();
}

bool UsbDirectDevice::SubmitTransfers(std::string* error) {
  const size_t data_buffer_bytes =
      static_cast<size_t>(packets_per_transfer_) * active_alt_.max_packet_bytes;
  for (uint32_t i = 0; i < kDataTransferCount; ++i) {
    std::unique_ptr<IsoTransfer> iso(new IsoTransfer());
    iso->device = this;
    iso->feedback = false;
    iso->buffer.assign(data_buffer_bytes, 0);
    iso->transfer =
        libusb_alloc_transfer(static_cast<int>(packets_per_transfer_));
    if (iso->transfer == nullptr) {
      SetError(error, "out of memory allocating isochronous transfers");
      return false;
    }
    libusb_fill_iso_transfer(iso->transfer, handle_, active_alt_.endpoint_address,
                             iso->buffer.data(),
                             static_cast<int>(iso->buffer.size()),
                             static_cast<int>(packets_per_transfer_),
                             &UsbDirectDevice::DataCallback, iso.get(),
                             kIsoTimeoutMs);
    FillDataTransfer(iso.get());
    iso->in_flight.store(true, std::memory_order_release);
    active_transfers_.fetch_add(1, std::memory_order_acq_rel);
    const int rc = libusb_submit_transfer(iso->transfer);
    if (rc != LIBUSB_SUCCESS) {
      iso->in_flight.store(false, std::memory_order_release);
      active_transfers_.fetch_sub(1, std::memory_order_acq_rel);
      libusb_free_transfer(iso->transfer);
      iso->transfer = nullptr;
      SetError(error, UsbError("submitting an isochronous OUT transfer", rc));
      return false;
    }
    transfers_.push_back(std::move(iso));
  }

  if (active_alt_.feedback_endpoint == 0) {
    return true;
  }
  for (uint32_t i = 0; i < kFeedbackTransferCount; ++i) {
    std::unique_ptr<IsoTransfer> iso(new IsoTransfer());
    iso->device = this;
    iso->feedback = true;
    iso->buffer.assign(feedback_packet_bytes_, 0);
    iso->transfer = libusb_alloc_transfer(1);
    if (iso->transfer == nullptr) {
      SetError(error, "out of memory allocating feedback transfers");
      return false;
    }
    libusb_fill_iso_transfer(iso->transfer, handle_,
                             active_alt_.feedback_endpoint, iso->buffer.data(),
                             static_cast<int>(iso->buffer.size()), 1,
                             &UsbDirectDevice::FeedbackCallback, iso.get(),
                             kIsoTimeoutMs);
    libusb_set_iso_packet_lengths(iso->transfer,
                                  static_cast<unsigned int>(feedback_packet_bytes_));
    iso->in_flight.store(true, std::memory_order_release);
    active_transfers_.fetch_add(1, std::memory_order_acq_rel);
    const int rc = libusb_submit_transfer(iso->transfer);
    if (rc != LIBUSB_SUCCESS) {
      iso->in_flight.store(false, std::memory_order_release);
      active_transfers_.fetch_sub(1, std::memory_order_acq_rel);
      libusb_free_transfer(iso->transfer);
      iso->transfer = nullptr;
      SetError(error, UsbError("submitting an isochronous feedback transfer", rc));
      return false;
    }
    transfers_.push_back(std::move(iso));
  }
  return true;
}

void UsbDirectDevice::FillDataTransfer(IsoTransfer* iso) {
  libusb_transfer* transfer = iso->transfer;
  uint32_t per_packet_q16 = nominal_service_q16_;
  const uint32_t feedback = feedback_q16_.load(std::memory_order_relaxed);
  if (feedback != 0) {
    per_packet_q16 = ClampToNominal(
        FeedbackToServiceIntervalQ16(feedback, high_speed_, active_alt_.interval),
        nominal_service_q16_, kFeedbackTolerancePercent);
  }

  RingBuffer* ring = ring_raw_;
  size_t offset = 0;
  uint64_t frames_queued = 0;
  uint64_t underruns = 0;
  for (int i = 0; i < transfer->num_iso_packets; ++i) {
    uint32_t frames = sizer_.NextFrames(per_packet_q16, max_frames_per_packet_);
    size_t bytes = static_cast<size_t>(frames) * frame_bytes_;
    if (offset + bytes > iso->buffer.size()) {
      bytes = iso->buffer.size() - offset;
      frames = static_cast<uint32_t>(bytes / frame_bytes_);
      bytes = static_cast<size_t>(frames) * frame_bytes_;
    }
    uint8_t* destination = iso->buffer.data() + offset;
    const size_t copied = ring == nullptr ? 0 : ring->Read(destination, bytes);
    if (copied > 0) {
      flowing_.store(true, std::memory_order_relaxed);
    }
    if (copied < bytes) {
      std::memset(destination + copied, 0, bytes - copied);
      if (flowing_.load(std::memory_order_relaxed)) {
        ++underruns;
      }
    }
    transfer->iso_packet_desc[i].length = static_cast<unsigned int>(bytes);
    offset += bytes;
    frames_queued += frames;
  }
  transfer->length = static_cast<int>(offset);
  packets_.fetch_add(static_cast<uint64_t>(transfer->num_iso_packets),
                     std::memory_order_relaxed);
  frames_.fetch_add(frames_queued, std::memory_order_relaxed);
  if (underruns > 0) {
    underruns_.fetch_add(underruns, std::memory_order_relaxed);
  }
}

void LIBUSB_CALL UsbDirectDevice::DataCallback(libusb_transfer* transfer) {
  IsoTransfer* iso = static_cast<IsoTransfer*>(transfer->user_data);
  iso->device->OnDataComplete(iso);
}

void LIBUSB_CALL UsbDirectDevice::FeedbackCallback(libusb_transfer* transfer) {
  IsoTransfer* iso = static_cast<IsoTransfer*>(transfer->user_data);
  iso->device->OnFeedbackComplete(iso);
}

void UsbDirectDevice::OnDataComplete(IsoTransfer* iso) {
  libusb_transfer* transfer = iso->transfer;
  const bool keep_going = running_.load(std::memory_order_acquire) &&
                          !stopping_.load(std::memory_order_acquire);
  if (keep_going && transfer->status != LIBUSB_TRANSFER_CANCELLED &&
      transfer->status != LIBUSB_TRANSFER_NO_DEVICE) {
    if (transfer->status != LIBUSB_TRANSFER_COMPLETED) {
      SetStreamError(Format("isochronous OUT transfer status %d",
                            static_cast<int>(transfer->status)));
    }
    FillDataTransfer(iso);
    const int rc = libusb_submit_transfer(transfer);
    if (rc == LIBUSB_SUCCESS) {
      return;
    }
    SetStreamError(UsbError("resubmitting an isochronous OUT transfer", rc));
  } else if (transfer->status == LIBUSB_TRANSFER_NO_DEVICE) {
    SetStreamError("the USB audio device went away");
  }

  iso->in_flight.store(false, std::memory_order_release);
  {
    std::lock_guard<std::mutex> lock(done_mutex_);
    active_transfers_.fetch_sub(1, std::memory_order_acq_rel);
  }
  done_cv_.notify_all();
}

void UsbDirectDevice::OnFeedbackComplete(IsoTransfer* iso) {
  libusb_transfer* transfer = iso->transfer;
  const bool keep_going = running_.load(std::memory_order_acquire) &&
                          !stopping_.load(std::memory_order_acquire);
  if (transfer->status == LIBUSB_TRANSFER_COMPLETED &&
      transfer->num_iso_packets > 0 &&
      transfer->iso_packet_desc[0].status == LIBUSB_TRANSFER_COMPLETED) {
    const size_t actual =
        static_cast<size_t>(transfer->iso_packet_desc[0].actual_length);
    const uint32_t raw =
        DecodeFeedbackQ16(iso->buffer.data(), actual, high_speed_);
    if (raw != 0) {
      feedback_q16_.store(
          ClampToNominal(raw, nominal_unit_q16_, kFeedbackTolerancePercent),
          std::memory_order_relaxed);
    }
  }
  if (keep_going && transfer->status != LIBUSB_TRANSFER_CANCELLED &&
      transfer->status != LIBUSB_TRANSFER_NO_DEVICE) {
    libusb_set_iso_packet_lengths(
        transfer, static_cast<unsigned int>(feedback_packet_bytes_));
    if (libusb_submit_transfer(transfer) == LIBUSB_SUCCESS) {
      return;
    }
  }

  iso->in_flight.store(false, std::memory_order_release);
  {
    std::lock_guard<std::mutex> lock(done_mutex_);
    active_transfers_.fetch_sub(1, std::memory_order_acq_rel);
  }
  done_cv_.notify_all();
}

void UsbDirectDevice::EventLoop() {
  while (!event_exit_.load(std::memory_order_acquire)) {
    struct timeval timeout;
    timeout.tv_sec = 0;
    timeout.tv_usec = 100000;
    int completed = 0;
    const int rc =
        libusb_handle_events_timeout_completed(context_, &timeout, &completed);
    if (rc != LIBUSB_SUCCESS && rc != LIBUSB_ERROR_INTERRUPTED) {
      // Keep pumping: the loop still has to reap the cancelled transfers, and
      // Stop() is what ends it.
      SetStreamError(UsbError("handling USB events", rc));
      std::this_thread::sleep_for(std::chrono::milliseconds(5));
    }
  }
}

int UsbDirectDevice::Write(const uint8_t* data, size_t length,
                           std::string* error) {
  if (data == nullptr) {
    SetError(error, "write buffer is null");
    return -1;
  }
  if (!running_.load(std::memory_order_acquire)) {
    SetError(error, "usb direct stream is not running");
    return -1;
  }
  std::shared_ptr<RingBuffer> ring;
  {
    std::lock_guard<std::mutex> lock(ring_mutex_);
    ring = ring_;
  }
  if (!ring) {
    SetError(error, "usb direct stream has no buffer");
    return -1;
  }
  return static_cast<int>(ring->Write(data, length));
}

std::string UsbDirectDevice::Status() const {
  size_t buffered = 0;
  {
    std::lock_guard<std::mutex> lock(ring_mutex_);
    if (ring_) {
      buffered = ring_->Available();
    }
  }
  std::string error;
  {
    std::lock_guard<std::mutex> lock(error_mutex_);
    error = stream_error_;
  }
  JsonWriter json;
  json.BeginObject();
  json.Field("running", running_.load(std::memory_order_acquire));
  json.Field("packets", packets_.load(std::memory_order_relaxed));
  json.Field("underruns", underruns_.load(std::memory_order_relaxed));
  json.Field("frames", frames_.load(std::memory_order_relaxed));
  json.Field("buffered_bytes", static_cast<uint64_t>(buffered));
  json.Field("feedback_rate",
             static_cast<int64_t>(FeedbackRateHz(
                 feedback_q16_.load(std::memory_order_relaxed), high_speed_)));
  json.Field("error", error);
  json.EndObject();
  return json.Result();
}

void UsbDirectDevice::SetStreamError(const std::string& message) {
  std::lock_guard<std::mutex> lock(error_mutex_);
  if (stream_error_.empty()) {
    stream_error_ = message;
  }
}

void UsbDirectDevice::Stop() {
  std::lock_guard<std::mutex> lock(state_mutex_);
  StopLocked();
}

void UsbDirectDevice::StopLocked() {
  const bool had_transfers = !transfers_.empty();
  if (!had_transfers && !event_thread_.joinable() && !streaming_configured_) {
    running_.store(false, std::memory_order_release);
    return;
  }

  stopping_.store(true, std::memory_order_release);
  running_.store(false, std::memory_order_release);

  for (const std::unique_ptr<IsoTransfer>& iso : transfers_) {
    if (iso->transfer != nullptr &&
        iso->in_flight.load(std::memory_order_acquire)) {
      libusb_cancel_transfer(iso->transfer);
    }
  }
  {
    std::unique_lock<std::mutex> lock(done_mutex_);
    done_cv_.wait_for(lock, std::chrono::milliseconds(kStopWaitMs), [this]() {
      return active_transfers_.load(std::memory_order_acquire) <= 0;
    });
  }
  if (active_transfers_.load(std::memory_order_acquire) > 0) {
    LogLine("%d isochronous transfers did not complete within %d ms",
            active_transfers_.load(std::memory_order_acquire), kStopWaitMs);
  }

  event_exit_.store(true, std::memory_order_release);
  if (event_thread_.joinable()) {
    event_thread_.join();
  }

  for (std::unique_ptr<IsoTransfer>& iso : transfers_) {
    if (iso->transfer == nullptr) {
      continue;
    }
    if (iso->in_flight.load(std::memory_order_acquire)) {
      // The device never returned this transfer. Deliberately leak it and its
      // buffer rather than let a late callback touch freed memory.
      LogLine("leaking one unreaped isochronous transfer");
      static_cast<void>(iso.release());
      continue;
    }
    libusb_free_transfer(iso->transfer);
  }
  transfers_.clear();
  {
    std::lock_guard<std::mutex> ring_lock(ring_mutex_);
    ring_raw_ = nullptr;
  }

  if (streaming_configured_ && handle_ != nullptr) {
    const int rc =
        libusb_set_interface_alt_setting(handle_, streaming_interface_, 0);
    if (rc != LIBUSB_SUCCESS && rc != LIBUSB_ERROR_NO_DEVICE) {
      LogLine("returning interface %u to alt setting 0 returned %d",
              static_cast<unsigned>(streaming_interface_), rc);
    }
  }
  streaming_configured_ = false;
  stopping_.store(false, std::memory_order_release);
}

void UsbDirectDevice::Close() {
  Stop();
  std::lock_guard<std::mutex> lock(state_mutex_);
  if (closed_) {
    return;
  }
  closed_ = true;
  if (handle_ != nullptr) {
    ReleaseInterfaces();
    libusb_close(handle_);
    handle_ = nullptr;
  }
  std::lock_guard<std::mutex> ring_lock(ring_mutex_);
  ring_.reset();
}

}  // namespace usb_direct
