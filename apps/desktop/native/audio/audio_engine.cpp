#include "audio_engine.hpp"

#include <algorithm>
#include <array>
#include <atomic>
#include <chrono>
#include <bit>
#include <cmath>
#include <condition_variable>
#include <cstring>
#include <limits>
#include <memory>
#include <mutex>
#include <optional>
#include <thread>
#include <type_traits>
#include <utility>

#ifdef _WIN32
#include <windows.h>
#include <initguid.h>
#include <audioclient.h>
#include <avrt.h>
#include <ks.h>
#include <ksmedia.h>
#include <mmdeviceapi.h>
#include <propkeydef.h>
#include <functiondiscoverykeys_devpkey.h>
#include <propvarutil.h>
#include <wrl/client.h>

extern "C" {
#include <libavutil/channel_layout.h>
#include <libavutil/samplefmt.h>
#include <libswresample/swresample.h>
}
#endif

namespace jastreamer {

EngineError::EngineError(std::string code, std::string message)
    : std::runtime_error(std::move(message)), code_(std::move(code)) {}

const std::string& EngineError::code() const noexcept { return code_; }

namespace {

template <typename T>
T clamp_scaled(long double value) {
    const auto low = static_cast<long double>(std::numeric_limits<T>::min());
    const auto high = static_cast<long double>(std::numeric_limits<T>::max());
    return static_cast<T>(std::clamp(std::nearbyint(value), low, high));
}

template <typename T>
void scale_integer(std::uint8_t* bytes, std::size_t samples, double gain) {
    for (std::size_t index = 0; index < samples; ++index) {
        T value{};
        std::memcpy(&value, bytes + index * sizeof(T), sizeof(T));
        value = clamp_scaled<T>(static_cast<long double>(value) * gain);
        std::memcpy(bytes + index * sizeof(T), &value, sizeof(T));
    }
}
template <typename T>
void clear_low_bits(std::uint8_t* bytes, std::size_t samples, int shift) {
    using Unsigned = std::make_unsigned_t<T>;
    const auto mask = static_cast<Unsigned>(~Unsigned{0} << shift);
    for (std::size_t index = 0; index < samples; ++index) {
        T value{};
        std::memcpy(&value, bytes + index * sizeof(T), sizeof(T));
        value = static_cast<T>(static_cast<Unsigned>(value) & mask);
        std::memcpy(bytes + index * sizeof(T), &value, sizeof(T));
    }
}

void scale_signed24(std::uint8_t* bytes, std::size_t samples, double gain) {
    for (std::size_t index = 0; index < samples; ++index) {
        auto* sample = bytes + index * 3;
        std::int32_t value = static_cast<std::int32_t>(
            static_cast<std::uint32_t>(sample[0]) |
            (static_cast<std::uint32_t>(sample[1]) << 8U) |
            (static_cast<std::uint32_t>(sample[2]) << 16U));
        if ((value & 0x00800000) != 0) value |= static_cast<std::int32_t>(0xff000000U);
        value = clamp_scaled<std::int32_t>(std::clamp(
            static_cast<long double>(value) * gain, -8388608.0L, 8388607.0L));
        sample[0] = static_cast<std::uint8_t>(value);
        sample[1] = static_cast<std::uint8_t>(static_cast<std::uint32_t>(value) >> 8U);
        sample[2] = static_cast<std::uint8_t>(static_cast<std::uint32_t>(value) >> 16U);
    }
}

void clear_signed24_low_bits(std::uint8_t* bytes, std::size_t samples, int shift) {
    const auto mask = static_cast<std::uint32_t>((0x00ffffffU << shift) & 0x00ffffffU);
    for (std::size_t index = 0; index < samples; ++index) {
        auto* sample = bytes + index * 3;
        auto value = static_cast<std::uint32_t>(sample[0]) |
                     (static_cast<std::uint32_t>(sample[1]) << 8U) |
                     (static_cast<std::uint32_t>(sample[2]) << 16U);
        value &= mask;
        sample[0] = static_cast<std::uint8_t>(value);
        sample[1] = static_cast<std::uint8_t>(value >> 8U);
        sample[2] = static_cast<std::uint8_t>(value >> 16U);
    }
}


template <typename T>
void scale_float(std::uint8_t* bytes, std::size_t samples, double gain) {
    for (std::size_t index = 0; index < samples; ++index) {
        T value{};
        std::memcpy(&value, bytes + index * sizeof(T), sizeof(T));
        const auto scaled = std::isfinite(value) ? value * static_cast<T>(gain) : static_cast<T>(0);
        value = std::clamp(scaled, static_cast<T>(-1), static_cast<T>(1));
        std::memcpy(bytes + index * sizeof(T), &value, sizeof(T));
    }
}

void validate_audio_format(const AudioFormat& format) {
    if (format.sample_rate <= 0 || format.sample_rate > 768000 || format.channels <= 0 ||
        format.channels > 32 || format.valid_bits <= 0 || format.valid_bits > format.container_bits) {
        throw EngineError("media_unsupported", "Decoded PCM format is not supported");
    }
    const bool valid_container = format.container_bits == 8 || format.container_bits == 16 ||
                                 format.container_bits == 24 || format.container_bits == 32 ||
                                 format.container_bits == 64;
    if (!valid_container || (format.floating_point && format.container_bits != 32 && format.container_bits != 64)) {
        throw EngineError("media_unsupported", "Decoded PCM container is not supported");
    }
}

bool same_audio_format(const AudioFormat& left, const AudioFormat& right) noexcept {
    return left.sample_rate == right.sample_rate &&
           left.channels == right.channels &&
           left.container_bits == right.container_bits &&
           left.valid_bits == right.valid_bits &&
           left.floating_point == right.floating_point &&
           left.channel_mask == right.channel_mask &&
           left.lossless == right.lossless;
}

} // namespace

namespace pcm {

void apply_gain(std::uint8_t* data, std::size_t frames, const AudioFormat& format, double gain) {
    validate_audio_format(format);
    if (!std::isfinite(gain) || gain < 0.0 || gain > 1.0) {
        throw EngineError("invalid_argument", "Volume must be between zero and one");
    }
    if (gain == 1.0 || frames == 0) {
        return;
    }
    const auto samples = frames * static_cast<std::size_t>(format.channels);
    if (format.floating_point) {
        if (format.container_bits == 32) scale_float<float>(data, samples, gain);
        else scale_float<double>(data, samples, gain);
        return;
    }
    switch (format.container_bits) {
    case 8:
        for (std::size_t index = 0; index < samples; ++index) {
            const auto centered = static_cast<int>(data[index]) - 128;
            data[index] = static_cast<std::uint8_t>(std::clamp(
                static_cast<int>(std::nearbyint(centered * gain)) + 128, 0, 255));
        }
        break;
    case 16: scale_integer<std::int16_t>(data, samples, gain); break;
    case 24: scale_signed24(data, samples, gain); break;
    case 32: scale_integer<std::int32_t>(data, samples, gain); break;
    case 64: scale_integer<std::int64_t>(data, samples, gain); break;
    default: throw EngineError("media_unsupported", "Decoded PCM container is not supported");
    }
    if (format.valid_bits < format.container_bits) {
        const auto shift = format.container_bits - format.valid_bits;
        switch (format.container_bits) {
        case 8: clear_low_bits<std::uint8_t>(data, samples, shift); break;
        case 16: clear_low_bits<std::int16_t>(data, samples, shift); break;
        case 24: clear_signed24_low_bits(data, samples, shift); break;
        case 32: clear_low_bits<std::int32_t>(data, samples, shift); break;
        case 64: clear_low_bits<std::int64_t>(data, samples, shift); break;
        default: break;
        }
    }
}

void validate_exclusive_source_format(const AudioFormat& format) {
    validate_audio_format(format);
    const auto block_align = static_cast<std::uint64_t>(format.channels) *
                             static_cast<std::uint64_t>(format.container_bits / 8);
    if (block_align == 0 || block_align > std::numeric_limits<std::uint16_t>::max()) {
        throw EngineError("exclusive_unsupported", "Exclusive PCM block alignment is not representable");
    }
}

std::uint32_t default_channel_mask(int channels) {
    static constexpr std::array<std::uint32_t, 9> masks{
        0,
        0x00000004U, // FC
        0x00000003U, // FL | FR
        0x00000007U, // FL | FR | FC
        0x00000033U, // quad
        0x00000607U, // 5.0
        0x0000060fU, // 5.1
        0x0000070fU, // 6.1
        0x0000063fU, // 7.1
    };
    return channels >= 1 && channels <= 8 ? masks[static_cast<std::size_t>(channels)] : 0;
}
std::int64_t exclusive_buffer_duration_100ns(std::uint32_t frames, int sample_rate) {
    if (frames == 0 || sample_rate <= 0) {
        throw EngineError("exclusive_unsupported", "Exclusive endpoint returned an invalid aligned buffer size");
    }
    return static_cast<std::int64_t>(
        (static_cast<std::uint64_t>(frames) * 10000000ULL +
         static_cast<std::uint64_t>(sample_rate) - 1) /
        static_cast<std::uint64_t>(sample_rate));
}


} // namespace pcm

#ifdef _WIN32
namespace {

using Microsoft::WRL::ComPtr;

std::string utf8(const wchar_t* value) {
    if (!value) return {};
    const auto length = static_cast<int>(wcslen(value));
    if (length == 0) return {};
    const auto needed = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value, length, nullptr, 0, nullptr, nullptr);
    if (needed <= 0) return {};
    std::string result(static_cast<std::size_t>(needed), '\0');
    WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value, length, result.data(), needed, nullptr, nullptr);
    return result;
}

std::wstring wide(const std::string& value) {
    if (value.empty()) return {};
    const auto needed = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(),
                                             static_cast<int>(value.size()), nullptr, 0);
    if (needed <= 0) throw EngineError("invalid_argument", "Device identifier is not valid UTF-8");
    std::wstring result(static_cast<std::size_t>(needed), L'\0');
    MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(), static_cast<int>(value.size()),
                        result.data(), needed);
    return result;
}

void check_hr(HRESULT result, const char* code, const char* message) {
    if (FAILED(result)) throw EngineError(code, message);
}

bool device_loss(HRESULT result) {
    return result == AUDCLNT_E_DEVICE_INVALIDATED || result == AUDCLNT_E_RESOURCES_INVALIDATED ||
           result == AUDCLNT_E_SERVICE_NOT_RUNNING || result == AUDCLNT_E_ENDPOINT_CREATE_FAILED;
}

std::string endpoint_name(IMMDevice* device) {
    ComPtr<IPropertyStore> properties;
    if (FAILED(device->OpenPropertyStore(STGM_READ, &properties))) return "Windows audio endpoint";
    PROPVARIANT value;
    PropVariantInit(&value);
    const auto result = properties->GetValue(PKEY_Device_FriendlyName, &value);
    std::string name = SUCCEEDED(result) && value.vt == VT_LPWSTR ? utf8(value.pwszVal) : "";
    PropVariantClear(&value);
    return name.empty() ? std::string("Windows audio endpoint") : name;
}

struct WaveFormat {
    WAVEFORMATEXTENSIBLE value{};

    WAVEFORMATEX* get() { return &value.Format; }
    const WAVEFORMATEX* get() const { return &value.Format; }
};

[[noreturn]] void throw_exclusive_failure(HRESULT result, const char* fallback) {
    if (result == AUDCLNT_E_DEVICE_IN_USE)
        throw EngineError("exclusive_busy", "Endpoint is busy in another application");
    if (result == AUDCLNT_E_EXCLUSIVE_MODE_NOT_ALLOWED)
        throw EngineError("exclusive_policy", "Windows policy does not allow exclusive endpoint access");
    if (result == AUDCLNT_E_UNSUPPORTED_FORMAT)
        throw EngineError("exclusive_unsupported", "Endpoint does not accept the exact decoded PCM format");
    if (device_loss(result))
        throw EngineError("device_unavailable", "Windows audio endpoint became unavailable");
    throw EngineError("exclusive_unsupported", fallback);
}

WaveFormat source_wave_format(const AudioFormat& source) {
    pcm::validate_exclusive_source_format(source);
    WaveFormat wave;
    wave.value.Format.wFormatTag = WAVE_FORMAT_EXTENSIBLE;
    wave.value.Format.nChannels = static_cast<WORD>(source.channels);
    wave.value.Format.nSamplesPerSec = static_cast<DWORD>(source.sample_rate);
    wave.value.Format.wBitsPerSample = static_cast<WORD>(source.container_bits);
    wave.value.Format.nBlockAlign = static_cast<WORD>(source.channels * source.container_bits / 8);
    wave.value.Format.nAvgBytesPerSec = wave.value.Format.nSamplesPerSec * wave.value.Format.nBlockAlign;
    wave.value.Format.cbSize = sizeof(WAVEFORMATEXTENSIBLE) - sizeof(WAVEFORMATEX);
    wave.value.Samples.wValidBitsPerSample = static_cast<WORD>(source.valid_bits);
    wave.value.dwChannelMask = source.channel_mask ? source.channel_mask : pcm::default_channel_mask(source.channels);
    wave.value.SubFormat = source.floating_point ? KSDATAFORMAT_SUBTYPE_IEEE_FLOAT : KSDATAFORMAT_SUBTYPE_PCM;
    return wave;
}

AudioFormat audio_format_from_wave(const WAVEFORMATEX* wave) {
    if (!wave || wave->nChannels == 0 || wave->nSamplesPerSec == 0 || wave->wBitsPerSample == 0) {
        throw EngineError("device_unavailable", "Endpoint returned an invalid mix format");
    }
    AudioFormat result{};
    result.sample_rate = static_cast<int>(wave->nSamplesPerSec);
    result.channels = static_cast<int>(wave->nChannels);
    result.container_bits = static_cast<int>(wave->wBitsPerSample);
    result.valid_bits = result.container_bits;
    result.channel_mask = pcm::default_channel_mask(result.channels);
    if (wave->wFormatTag == WAVE_FORMAT_IEEE_FLOAT) {
        result.floating_point = true;
    } else if (wave->wFormatTag == WAVE_FORMAT_EXTENSIBLE &&
               wave->cbSize >= sizeof(WAVEFORMATEXTENSIBLE) - sizeof(WAVEFORMATEX)) {
        const auto* extensible = reinterpret_cast<const WAVEFORMATEXTENSIBLE*>(wave);
        result.valid_bits = extensible->Samples.wValidBitsPerSample
            ? extensible->Samples.wValidBitsPerSample
            : result.container_bits;
        result.channel_mask = extensible->dwChannelMask;
        if (extensible->SubFormat == KSDATAFORMAT_SUBTYPE_IEEE_FLOAT) result.floating_point = true;
        else if (extensible->SubFormat != KSDATAFORMAT_SUBTYPE_PCM)
            throw EngineError("device_unavailable", "Endpoint mix format is not PCM");
    } else if (wave->wFormatTag != WAVE_FORMAT_PCM) {
        throw EngineError("device_unavailable", "Endpoint mix format is not PCM");
    }
    result.lossless = false;
    validate_audio_format(result);
    if (!result.floating_point && result.container_bits == 64)
        throw EngineError("device_unavailable", "Endpoint shared mix format uses unsupported 64-bit integer PCM");
    return result;
}

AVSampleFormat av_sample_format(const AudioFormat& format) {
    if (format.floating_point) return format.container_bits == 32 ? AV_SAMPLE_FMT_FLT : AV_SAMPLE_FMT_DBL;
    switch (format.container_bits) {
    case 8: return AV_SAMPLE_FMT_U8;
    case 16: return AV_SAMPLE_FMT_S16;
    case 32: return AV_SAMPLE_FMT_S32;
    case 64: return AV_SAMPLE_FMT_S64;
    default: return AV_SAMPLE_FMT_NONE;
    }
}

class ByteRing final {
public:
    explicit ByteRing(std::size_t capacity) : bytes_(capacity) {}

    std::size_t available() const noexcept {
        return static_cast<std::size_t>(write_.load(std::memory_order_acquire) - read_.load(std::memory_order_acquire));
    }
    std::size_t free_space() const noexcept { return bytes_.size() - available(); }

    std::size_t write(const std::uint8_t* data, std::size_t count) noexcept {
        count = std::min(count, free_space());
        const auto position = write_.load(std::memory_order_relaxed);
        const auto offset = static_cast<std::size_t>(position % bytes_.size());
        const auto first = std::min(count, bytes_.size() - offset);
        std::memcpy(bytes_.data() + offset, data, first);
        std::memcpy(bytes_.data(), data + first, count - first);
        write_.store(position + count, std::memory_order_release);
        return count;
    }

    std::size_t read(std::uint8_t* destination, std::size_t count) noexcept {
        count = std::min(count, available());
        const auto position = read_.load(std::memory_order_relaxed);
        const auto offset = static_cast<std::size_t>(position % bytes_.size());
        const auto first = std::min(count, bytes_.size() - offset);
        std::memcpy(destination, bytes_.data() + offset, first);
        std::memcpy(destination + first, bytes_.data(), count - first);
        read_.store(position + count, std::memory_order_release);
        return count;
    }

    void clear() noexcept {
        const auto position = write_.load(std::memory_order_acquire);
        read_.store(position, std::memory_order_release);
    }

private:
    std::vector<std::uint8_t> bytes_;
    std::atomic<std::uint64_t> read_{0};
    std::atomic<std::uint64_t> write_{0};
};

class SharedConverter final {
public:
    SharedConverter(const AudioFormat& input, const AudioFormat& output)
        : input_(input), output_(output) {
        AVChannelLayout input_layout{};
        AVChannelLayout output_layout{};
        if (input.channel_mask && av_channel_layout_from_mask(&input_layout, input.channel_mask) >= 0 &&
            input_layout.nb_channels != input.channels) av_channel_layout_uninit(&input_layout);
        if (input_layout.nb_channels == 0) av_channel_layout_default(&input_layout, input.channels);
        if (output.channel_mask && av_channel_layout_from_mask(&output_layout, output.channel_mask) >= 0 &&
            output_layout.nb_channels != output.channels) av_channel_layout_uninit(&output_layout);
        if (output_layout.nb_channels == 0) av_channel_layout_default(&output_layout, output.channels);
        const auto input_sample = av_sample_format(input);
        output_sample_ = output.floating_point
            ? (output.container_bits == 32 ? AV_SAMPLE_FMT_FLT : AV_SAMPLE_FMT_DBL)
            : (output.container_bits == 8 ? AV_SAMPLE_FMT_U8 : output.container_bits == 16 ? AV_SAMPLE_FMT_S16 : AV_SAMPLE_FMT_S32);
        const auto result = swr_alloc_set_opts2(&context_, &output_layout, output_sample_, output.sample_rate,
                                                &input_layout, input_sample, input.sample_rate, 0, nullptr);
        av_channel_layout_uninit(&input_layout);
        av_channel_layout_uninit(&output_layout);
        if (result < 0 || !context_ || swr_init(context_) < 0) {
            if (context_) swr_free(&context_);
            throw EngineError("device_unavailable", "Cannot create shared-mode PCM conversion");
        }
    }

    ~SharedConverter() { swr_free(&context_); }

    std::pair<const std::uint8_t*, std::size_t> convert(const std::uint8_t* input, std::size_t frames) {
        const auto capacity = swr_get_out_samples(context_, static_cast<int>(frames));
        if (capacity < 0) throw EngineError("media_error", "Shared PCM conversion failed");
        const auto staging_bits = output_.floating_point ? output_.container_bits :
                                  (output_.container_bits <= 16 ? output_.container_bits : 32);
        const auto staging_stride = static_cast<std::size_t>(staging_bits / 8 * output_.channels);
        converted_.resize(static_cast<std::size_t>(capacity) * staging_stride);
        auto* output_data = converted_.data();
        const std::uint8_t* input_data = input;
        const std::uint8_t** input_planes = input ? &input_data : nullptr;
        const auto produced = swr_convert(context_, &output_data, capacity, input_planes, static_cast<int>(frames));
        if (produced < 0) throw EngineError("media_error", "Shared PCM conversion failed");
        auto bytes = static_cast<std::size_t>(produced) * staging_stride;
        if (!output_.floating_point && output_.container_bits > output_.valid_bits) {
            const auto count = static_cast<std::size_t>(produced) * output_.channels;
            if (output_.container_bits == 8) {
                const auto shift = output_.container_bits - output_.valid_bits;
                const auto mask = static_cast<std::uint8_t>(0xffU << shift);
                for (std::size_t index = 0; index < count; ++index) converted_[index] &= mask;
            } else if (output_.container_bits == 16) {
                const auto shift = output_.container_bits - output_.valid_bits;
                const auto mask = static_cast<std::uint16_t>(0xffffU << shift);
                auto* words = reinterpret_cast<std::uint16_t*>(converted_.data());
                for (std::size_t index = 0; index < count; ++index) words[index] &= mask;
            } else {
                const auto shift = 32 - output_.valid_bits;
                const auto mask = static_cast<std::uint32_t>(~0U << shift);
                auto* words = reinterpret_cast<std::uint32_t*>(converted_.data());
                for (std::size_t index = 0; index < count; ++index) words[index] &= mask;
            }
        }
        if (!output_.floating_point && output_.container_bits == 24) {
            const auto count = static_cast<std::size_t>(produced) * output_.channels;
            packed_.resize(count * 3);
            const auto* words = reinterpret_cast<const std::int32_t*>(converted_.data());
            for (std::size_t index = 0; index < count; ++index) {
                const auto word = static_cast<std::uint32_t>(words[index]);
                packed_[index * 3] = static_cast<std::uint8_t>(word >> 8);
                packed_[index * 3 + 1] = static_cast<std::uint8_t>(word >> 16);
                packed_[index * 3 + 2] = static_cast<std::uint8_t>(word >> 24);
            }
            return {packed_.data(), packed_.size()};
        }
        return {converted_.data(), bytes};
    }

    std::pair<const std::uint8_t*, std::size_t> flush() { return convert(nullptr, 0); }

    void reset() {
        swr_close(context_);
        if (swr_init(context_) < 0) {
            throw EngineError("media_error", "Shared PCM conversion could not reset after seeking");
        }
        converted_.clear();
        packed_.clear();
    }

private:
    AudioFormat input_;
    AudioFormat output_;
    SwrContext* context_ = nullptr;
    AVSampleFormat output_sample_ = AV_SAMPLE_FMT_NONE;
    std::vector<std::uint8_t> converted_;
    std::vector<std::uint8_t> packed_;
};

struct OpenedEndpoint {
    ComPtr<IMMDevice> device;
    ComPtr<IAudioClient> client;
    ComPtr<IAudioRenderClient> render;
    ComPtr<IAudioClock> clock;
    HANDLE event = nullptr;
    bool event_driven = false;
    UINT32 buffer_frames = 0;
    AudioFormat output_format{};
    std::string id;
    std::string name;
};

OpenedEndpoint open_endpoint(const std::string& requested_id, bool exclusive, const AudioFormat& source) {
    ComPtr<IMMDeviceEnumerator> enumerator;
    check_hr(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL,
                              IID_PPV_ARGS(&enumerator)), "device_unavailable", "Windows audio service is unavailable");
    ComPtr<IMMDevice> device;
    HRESULT result = S_OK;
    if (requested_id == "default") {
        result = enumerator->GetDefaultAudioEndpoint(eRender, eMultimedia, &device);
    } else {
        const auto id = wide(requested_id);
        result = enumerator->GetDevice(id.c_str(), &device);
    }
    if (FAILED(result)) throw EngineError("device_not_found", "Requested Windows audio endpoint is unavailable");

    LPWSTR resolved_id = nullptr;
    check_hr(device->GetId(&resolved_id), "device_unavailable", "Cannot identify Windows audio endpoint");
    const auto id = utf8(resolved_id);
    CoTaskMemFree(resolved_id);

    ComPtr<IAudioClient> client;
    check_hr(device->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr,
                              reinterpret_cast<void**>(client.GetAddressOf())),
             "device_unavailable", "Cannot activate Windows audio endpoint");

    WAVEFORMATEX* selected = nullptr;
    WaveFormat exact;
    AudioFormat output{};
    AUDCLNT_SHAREMODE mode = exclusive ? AUDCLNT_SHAREMODE_EXCLUSIVE : AUDCLNT_SHAREMODE_SHARED;
    if (exclusive) {
        exact = source_wave_format(source);
        WAVEFORMATEX* closest = nullptr;
        result = client->IsFormatSupported(mode, exact.get(), &closest);
        if (closest) CoTaskMemFree(closest);
        if (result != S_OK)
            throw_exclusive_failure(result, "Endpoint does not accept the exact decoded PCM format");
        selected = exact.get();
        output = source;
    } else {
        check_hr(client->GetMixFormat(&selected), "device_unavailable", "Cannot read endpoint shared mix format");
        output = audio_format_from_wave(selected);
    }

    // Shared streams are event driven. Exclusive streams use push (timer) mode, which every
    // exclusive driver supports: event-driven exclusive rendered silence on a FiiO K17 while
    // the rate locked and the clock advanced, and shared mode played normally.
    const DWORD flags = exclusive ? AUDCLNT_STREAMFLAGS_NOPERSIST
                                  : AUDCLNT_STREAMFLAGS_EVENTCALLBACK | AUDCLNT_STREAMFLAGS_NOPERSIST;
    if (exclusive) {
        REFERENCE_TIME buffer_duration = 2000000;  // 200 ms
        result = client->Initialize(mode, flags, buffer_duration, 0, selected, nullptr);
        if (result == AUDCLNT_E_BUFFER_SIZE_NOT_ALIGNED) {
            UINT32 aligned_frames = 0;
            check_hr(client->GetBufferSize(&aligned_frames), "exclusive_unsupported",
                     "Cannot determine aligned exclusive buffer size");
            buffer_duration = static_cast<REFERENCE_TIME>(
                pcm::exclusive_buffer_duration_100ns(aligned_frames, selected->nSamplesPerSec));
            client.Reset();
            check_hr(device->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr,
                                      reinterpret_cast<void**>(client.GetAddressOf())),
                     "device_unavailable", "Cannot reactivate Windows audio endpoint");
            result = client->Initialize(mode, flags, buffer_duration, 0, selected, nullptr);
        }
    } else {
        ComPtr<IAudioClient3> client3;
        if (SUCCEEDED(client.As(&client3))) {
            UINT32 default_frames = 0, fundamental_frames = 0, minimum_frames = 0, maximum_frames = 0;
            if (SUCCEEDED(client3->GetSharedModeEnginePeriod(selected, &default_frames, &fundamental_frames,
                                                             &minimum_frames, &maximum_frames))) {
                // The documented InitializeSharedAudioStream contract supports only EVENTCALLBACK;
                // passing NOPERSIST here rejects shared initialization.
                result = client3->InitializeSharedAudioStream(AUDCLNT_STREAMFLAGS_EVENTCALLBACK, default_frames,
                                                              selected, nullptr);
            } else {
                result = client->Initialize(mode, flags, 0, 0, selected, nullptr);
            }
        } else {
            result = client->Initialize(mode, flags, 0, 0, selected, nullptr);
        }
    }
    if (!exclusive) CoTaskMemFree(selected);
    if (FAILED(result)) {
        if (exclusive) throw_exclusive_failure(result, "Endpoint rejected exact exclusive initialization");
        if (device_loss(result))
            throw EngineError("device_unavailable", "Windows audio endpoint became unavailable");
        throw EngineError("device_unavailable", "Endpoint rejected shared initialization");
    }

    // The event wakes the render thread: signalled by WASAPI for shared streams, and only
    // manually (seek/stop) for push-mode exclusive streams, which poll instead.
    HANDLE event = CreateEventW(nullptr, FALSE, FALSE, nullptr);
    if (!event) throw EngineError("audio_error", "Cannot create WASAPI render event");
    if (!exclusive && FAILED(client->SetEventHandle(event))) {
        CloseHandle(event);
        throw EngineError("audio_error", "Cannot configure WASAPI render event");
    }
    UINT32 frames = 0;
    if (FAILED(client->GetBufferSize(&frames)) || frames == 0) {
        CloseHandle(event);
        throw EngineError("audio_error", "Cannot determine WASAPI buffer size");
    }
    ComPtr<IAudioRenderClient> render;
    if (FAILED(client->GetService(IID_PPV_ARGS(&render)))) {
        CloseHandle(event);
        throw EngineError("audio_error", "Cannot obtain WASAPI render service");
    }
    ComPtr<IAudioClock> clock;
    if (FAILED(client->GetService(IID_PPV_ARGS(&clock)))) {
        CloseHandle(event);
        throw EngineError("audio_error", "Cannot obtain WASAPI endpoint clock");
    }
    const auto name = endpoint_name(device.Get());
    return {std::move(device), std::move(client), std::move(render), std::move(clock), event, !exclusive,
            frames, output, id, name};
}


nlohmann::json observation(std::string event, const std::string& play_id, std::string state,
                           std::int64_t position, std::int64_t duration, bool has_position) {
    nlohmann::json result{{"event", std::move(event)}, {"play_id", play_id},
                          {"position_ms", position}, {"duration_ms", duration},
                          {"has_position", has_position}};
    if (!state.empty()) result["state"] = std::move(state);
    return result;
}

} // namespace

class AudioEngine::Impl final {
public:
    explicit Impl(Emit callback) : emit(std::move(callback)) {}
    ~Impl() { shutdown(); }

    enum class Terminal { none, ended, media_failure, device_lost, audio_error };

    class Session final : public std::enable_shared_from_this<Session> {
    public:
        Session(Impl& owner, std::uint64_t token_value, MediaSpec media)
            : parent(owner), token(token_value), spec(std::move(media)),
              decoder(std::make_unique<Decoder>(spec.source.read, spec.size, spec.seekable)),
              source_format(decoder->format()),
              duration(decoder->duration_ms() > 0
                           ? decoder->duration_ms()
                           : std::max<std::int64_t>(0, spec.declared_duration_ms)),
              endpoint(open_endpoint(spec.device_id, spec.exclusive, source_format)),
              output_bytes_per_frame(static_cast<std::size_t>(
                  endpoint.output_format.channels * endpoint.output_format.container_bits / 8)),
              ring(std::max<std::size_t>({output_bytes_per_frame * endpoint.output_format.sample_rate / 4ULL,
                                          output_bytes_per_frame * endpoint.buffer_frames * 4ULL,
                                          output_bytes_per_frame * 4096ULL})) {
            validate_audio_format(source_format);
            if (!spec.exclusive) converter = std::make_unique<SharedConverter>(source_format, endpoint.output_format);
            gain.store(spec.volume, std::memory_order_relaxed);
            check_hr(endpoint.clock->GetFrequency(&clock_frequency), "audio_error",
                     "Cannot read WASAPI endpoint clock frequency");
            if (clock_frequency == 0) throw EngineError("audio_error", "WASAPI endpoint clock frequency is invalid");
        }
        ~Session() { stop_and_join(); }

        void start_threads() {
            try {
                render_thread = std::thread([self = shared_from_this()] { self->render_loop(); });
                decode_thread = std::thread([self = shared_from_this()] { self->decode_loop(); });
                monitor_thread = std::thread([self = shared_from_this()] { self->monitor_loop(); });
            } catch (...) {
                stop_and_join();
                throw EngineError("audio_error", "Cannot start native audio worker threads");
            }
        }

        void start_playback() {
            std::lock_guard lock(resource_mutex);
            if (!endpoint.client) throw EngineError("audio_error", "Audio endpoint is no longer active");
            begin_clock_segment();
            const auto result = endpoint.client->Start();
            if (FAILED(result)) {
                clock_running.store(false, std::memory_order_release);
                if (device_loss(result)) throw EngineError("device_lost", "Windows audio endpoint was lost");
                throw EngineError("audio_error", "WASAPI playback could not start");
            }
            playing.store(true, std::memory_order_release);
        }

        void pause_playback() {
            playing.store(false, std::memory_order_release);
            std::lock_guard lock(resource_mutex);
            if (!endpoint.client) throw EngineError("audio_error", "Audio endpoint is no longer active");
            const auto result = endpoint.client->Stop();
            if (FAILED(result)) {
                if (device_loss(result)) throw EngineError("device_lost", "Windows audio endpoint was lost");
                throw EngineError("audio_error", "WASAPI playback could not pause");
            }
            refresh_clock();
            clock_running.store(false, std::memory_order_release);
        }

        std::int64_t seek_to(std::int64_t position_ms) {
            if (!spec.seekable) throw EngineError("action_failed", "Media source is not seekable");
            if (position_ms < 0)
                throw EngineError("invalid_argument", "Seek position cannot be negative");
            const auto target_position =
                duration > 0 ? std::min(position_ms, duration) : position_ms;
            const bool resume = playing.exchange(false, std::memory_order_acq_rel);
            {
                std::lock_guard lock(wake_mutex);
                if (stopping.load(std::memory_order_acquire) ||
                    terminal.load(std::memory_order_acquire) != Terminal::none) {
                    throw EngineError("audio_error", "Audio session is no longer active");
                }
                seek_requested = true;
            }
            {
                std::lock_guard lock(resource_mutex);
                if (endpoint.event) SetEvent(endpoint.event);
            }
            wake.notify_all();
            try {
                if (spec.source.cancel) spec.source.cancel();
            } catch (...) {
                finish_seek();
                throw EngineError("media_error", "Media source could not interrupt a pending read");
            }
            {
                std::unique_lock lock(wake_mutex);
                wake.wait(lock, [&] {
                    return stopping.load(std::memory_order_acquire) ||
                           terminal.load(std::memory_order_acquire) != Terminal::none ||
                           (decode_quiescent && render_quiescent);
                });
                if (stopping.load(std::memory_order_acquire) ||
                    terminal.load(std::memory_order_acquire) != Terminal::none) {
                    seek_requested = false;
                    lock.unlock();
                    wake.notify_all();
                    throw EngineError("audio_error", "Audio session ended while seeking");
                }
            }

            std::int64_t actual_position = 0;
            try {
                {
                    std::lock_guard resource_lock(resource_mutex);
                    if (!endpoint.client)
                        throw EngineError("audio_error", "Audio endpoint is no longer active");
                    check_hr(endpoint.client->Stop(), "audio_error",
                             "WASAPI playback could not pause for seeking");
                    clock_running.store(false, std::memory_order_release);
                }
                try {
                    spec.source.rearm();
                } catch (...) {
                    throw EngineError("media_error", "Media source could not resume after seeking");
                }
                {
                    std::lock_guard decode_lock(decoder_mutex);
                    try {
                        auto replacement =
                            std::make_unique<Decoder>(spec.source.read, spec.size, spec.seekable);
                        if (!same_audio_format(replacement->format(), source_format)) {
                            throw EngineError("media_error",
                                              "Decoded PCM format changed while seeking");
                        }
                        replacement->seek(target_position);
                        actual_position = replacement->position_ms();
                        decoder = std::move(replacement);
                    } catch (const SourceError&) {
                        throw EngineError("media_error", "Media source failed while seeking");
                    } catch (const DecodeError&) {
                        throw EngineError("media_error", "Decoder could not seek to the requested position");
                    }
                    if (converter) converter->reset();
                    ring.clear();
                    end_of_stream.store(false, std::memory_order_release);
                    base_position_ms.store(actual_position, std::memory_order_release);
                    consumed_device_frames.store(0, std::memory_order_release);
                    consumed_audio_frames.store(0, std::memory_order_release);
                    submitted_device_frames.store(0, std::memory_order_release);
                    submitted_audio_frames.store(0, std::memory_order_release);
                    submission_read.store(0, std::memory_order_release);
                    submission_write.store(0, std::memory_order_release);
                    end_device_frame.store(std::numeric_limits<std::uint64_t>::max(),
                                           std::memory_order_release);
                }
                {
                    std::lock_guard resource_lock(resource_mutex);
                    check_hr(endpoint.client->Reset(), "audio_error",
                             "WASAPI playback could not reset after seeking");
                    if (resume) {
                        begin_clock_segment();
                        check_hr(endpoint.client->Start(), "audio_error",
                                 "WASAPI playback could not resume after seeking");
                    }
                }
            } catch (...) {
                finish_seek();
                throw;
            }
            playing.store(resume, std::memory_order_release);
            finish_seek();
            return actual_position;
        }

        void set_gain(double value) { gain.store(value, std::memory_order_release); }

        std::int64_t position_ms() const {
            const auto frames = consumed_audio_frames.load(std::memory_order_acquire);
            const auto elapsed = static_cast<std::int64_t>(frames * 1000ULL / endpoint.output_format.sample_rate);
            const auto value = base_position_ms.load(std::memory_order_acquire) + elapsed;
            return duration > 0 ? std::min(value, duration) : value;
        }

        bool transparent() const {
            return spec.exclusive && spec.transformed_known && !spec.transformed && source_format.lossless &&
                   gain.load(std::memory_order_acquire) == 1.0 && !samples_changed.load(std::memory_order_acquire) &&
                   source_format.sample_rate == endpoint.output_format.sample_rate &&
                   source_format.channels == endpoint.output_format.channels &&
                   source_format.container_bits == endpoint.output_format.container_bits &&
                   source_format.valid_bits == endpoint.output_format.valid_bits &&
                   source_format.channel_mask == endpoint.output_format.channel_mask;
        }

        std::string transparency_reason() const {
            if (!spec.exclusive) return "Shared mode uses the endpoint mix format and may convert samples";
            if (!spec.transformed_known) return "Source provenance is unknown";
            if (spec.transformed) return "Server reports transformed media";
            if (!source_format.lossless) return "Decoder reports a lossy source";
            if (gain.load(std::memory_order_acquire) != 1.0 || samples_changed.load(std::memory_order_acquire))
                return "App-local gain changed decoded samples";
            return "Exclusive exact format with original lossless media and unchanged unity-gain samples";
        }

        void request_stop() noexcept {
            {
                std::lock_guard lock(wake_mutex);
                stopping.store(true, std::memory_order_release);
            }
            playing.store(false, std::memory_order_release);
            try {
                if (spec.source.cancel) spec.source.cancel();
            } catch (...) {
            }
            {
                std::lock_guard lock(resource_mutex);
                if (endpoint.client) {
                    endpoint.client->Stop();
                    refresh_clock();
                }
                if (endpoint.event) SetEvent(endpoint.event);
            }
            wake.notify_all();
        }

        void stop_and_join() noexcept {
            request_stop();
            if (render_thread.joinable() && render_thread.get_id() != std::this_thread::get_id()) render_thread.join();
            {
                std::lock_guard lock(resource_mutex);
                refresh_clock();
                clock_running.store(false, std::memory_order_release);
            }
            if (decode_thread.joinable() && decode_thread.get_id() != std::this_thread::get_id()) decode_thread.join();
            if (monitor_thread.joinable() && monitor_thread.get_id() != std::this_thread::get_id()) monitor_thread.join();
            release_endpoint();
        }

        Impl& parent;
        const std::uint64_t token;
        MediaSpec spec;
        std::unique_ptr<Decoder> decoder;
        AudioFormat source_format;
        std::int64_t duration;
        OpenedEndpoint endpoint;
        const std::size_t output_bytes_per_frame;
        ByteRing ring;
        std::unique_ptr<SharedConverter> converter;
        std::atomic<double> gain{1.0};
        std::atomic<bool> samples_changed{false};

    private:
        struct Submission {
            std::uint64_t start_device_frame = 0;
            std::uint64_t audio_before = 0;
            std::uint32_t total_frames = 0;
            std::uint32_t audio_frames = 0;
        };

        bool quiesce_for_seek(bool& quiescent) noexcept {
            std::unique_lock lock(wake_mutex);
            if (!seek_requested) return false;
            quiescent = true;
            wake.notify_all();
            wake.wait(lock, [&] {
                return stopping.load(std::memory_order_acquire) ||
                       terminal.load(std::memory_order_acquire) != Terminal::none ||
                       !seek_requested;
            });
            quiescent = false;
            wake.notify_all();
            return true;
        }

        bool seek_pending() noexcept {
            std::lock_guard lock(wake_mutex);
            return seek_requested;
        }

        void finish_seek() noexcept {
            {
                std::lock_guard lock(wake_mutex);
                seek_requested = false;
            }
            wake.notify_all();
        }

        void update_audio_consumption(std::uint64_t device_frames) noexcept {
            auto read_index = submission_read.load(std::memory_order_relaxed);
            const auto write_index = submission_write.load(std::memory_order_acquire);
            while (read_index < write_index) {
                const auto& item = submissions[read_index % submissions.size()];
                if (device_frames <= item.start_device_frame) break;
                const auto progress = std::min<std::uint64_t>(
                    device_frames - item.start_device_frame, item.total_frames);
                consumed_audio_frames.store(
                    item.audio_before + std::min<std::uint64_t>(progress, item.audio_frames),
                    std::memory_order_release);
                if (progress < item.total_frames) break;
                ++read_index;
            }
            submission_read.store(read_index, std::memory_order_release);
        }

        bool record_submission(std::uint64_t start, std::uint32_t total, std::uint32_t audio,
                               std::uint64_t audio_before) noexcept {
            const auto write_index = submission_write.load(std::memory_order_relaxed);
            if (write_index - submission_read.load(std::memory_order_acquire) >= submissions.size()) return false;
            submissions[write_index % submissions.size()] = {start, audio_before, total, audio};
            submission_write.store(write_index + 1, std::memory_order_release);
            return true;
        }

        void begin_clock_segment() {
            UINT64 position = 0;
            UINT64 qpc = 0;
            check_hr(endpoint.clock->GetPosition(&position, &qpc), "audio_error",
                     "Cannot read WASAPI endpoint clock");
            clock_origin.store(position, std::memory_order_release);
            clock_segment_base.store(consumed_device_frames.load(std::memory_order_acquire), std::memory_order_release);
            clock_running.store(true, std::memory_order_release);
        }

        void refresh_clock() noexcept {
            if (!clock_running.load(std::memory_order_acquire) || !endpoint.clock) return;
            if (clock_update.test_and_set(std::memory_order_acquire)) return;
            UINT64 position = 0;
            UINT64 qpc = 0;
            if (SUCCEEDED(endpoint.clock->GetPosition(&position, &qpc))) {
                const auto origin = clock_origin.load(std::memory_order_acquire);
                const auto elapsed_ticks = position >= origin ? position - origin : 0;
                const auto elapsed_frames = elapsed_ticks * endpoint.output_format.sample_rate / clock_frequency;
                const auto consumed =
                    clock_segment_base.load(std::memory_order_acquire) + elapsed_frames;
                consumed_device_frames.store(consumed, std::memory_order_release);
                update_audio_consumption(consumed);
            }
            clock_update.clear(std::memory_order_release);
        }

        void release_endpoint() noexcept {
            std::lock_guard lock(resource_mutex);
            if (endpoint.client) endpoint.client->Stop();
            endpoint.clock.Reset();
            endpoint.render.Reset();
            endpoint.client.Reset();
            endpoint.device.Reset();
            if (endpoint.event) {
                CloseHandle(endpoint.event);
                endpoint.event = nullptr;
            }
        }

        void set_terminal(Terminal value, const char* message) noexcept {
            bool changed = false;
            {
                std::lock_guard lock(wake_mutex);
                auto expected = Terminal::none;
                changed = terminal.compare_exchange_strong(expected, value, std::memory_order_acq_rel);
                if (changed) terminal_message.store(message, std::memory_order_release);
            }
            if (changed) wake.notify_all();
        }

        void render_loop() noexcept {
            CoInitializeEx(nullptr, COINIT_MULTITHREADED);
            DWORD task_index = 0;
            HANDLE mmcss = AvSetMmThreadCharacteristicsW(L"Pro Audio", &task_index);
            while (!stopping.load(std::memory_order_acquire) && terminal.load(std::memory_order_acquire) == Terminal::none) {
                if (quiesce_for_seek(render_quiescent)) continue;
                // Push-mode (exclusive) streams poll every 10 ms; the event only wakes them for
                // seek/stop. Event-driven (shared) streams wait for the WASAPI signal.
                const auto wait = WaitForSingleObject(endpoint.event, endpoint.event_driven ? 500 : 10);
                if (stopping.load(std::memory_order_acquire)) break;
                if (wait != WAIT_OBJECT_0 && !(wait == WAIT_TIMEOUT && !endpoint.event_driven)) continue;
                if (quiesce_for_seek(render_quiescent)) continue;
                if (!playing.load(std::memory_order_acquire)) continue;
                refresh_clock();
                if (end_of_stream.load(std::memory_order_acquire) && ring.available() == 0) {
                    auto target = end_device_frame.load(std::memory_order_acquire);
                    if (target == std::numeric_limits<std::uint64_t>::max()) {
                        target = submitted_device_frames.load(std::memory_order_acquire);
                        end_device_frame.store(target, std::memory_order_release);
                    }
                    if (consumed_device_frames.load(std::memory_order_acquire) >= target) {
                        set_terminal(Terminal::ended, "Playback reached end of media");
                        break;
                    }
                    continue;
                }
                UINT32 padding = 0;
                HRESULT result = endpoint.client->GetCurrentPadding(&padding);
                if (FAILED(result)) {
                    set_terminal(device_loss(result) ? Terminal::device_lost : Terminal::audio_error,
                                 device_loss(result) ? "Windows audio endpoint was lost" : "WASAPI padding query failed");
                    break;
                }
                if (padding >= endpoint.buffer_frames) continue;
                const UINT32 requested_frames = endpoint.buffer_frames - padding;
                BYTE* destination = nullptr;
                result = endpoint.render->GetBuffer(requested_frames, &destination);
                if (FAILED(result)) {
                    set_terminal(device_loss(result) ? Terminal::device_lost : Terminal::audio_error,
                                 device_loss(result) ? "Windows audio endpoint was lost" : "WASAPI render buffer failed");
                    break;
                }
                const auto requested_bytes = static_cast<std::size_t>(requested_frames) * output_bytes_per_frame;
                auto copied = ring.read(destination, requested_bytes);
                copied -= copied % output_bytes_per_frame;
                const auto copied_frames = static_cast<UINT32>(copied / output_bytes_per_frame);
                if (copied > 0 && copied < requested_bytes) {
                    const auto silence = !endpoint.output_format.floating_point &&
                                         endpoint.output_format.container_bits == 8 ? 0x80 : 0;
                    std::memset(destination + copied, silence, requested_bytes - copied);
                }
                const DWORD release_flags = copied == 0 ? AUDCLNT_BUFFERFLAGS_SILENT : 0;
                result = endpoint.render->ReleaseBuffer(requested_frames, release_flags);
                if (FAILED(result)) {
                    set_terminal(device_loss(result) ? Terminal::device_lost : Terminal::audio_error,
                                 device_loss(result) ? "Windows audio endpoint was lost" : "WASAPI render release failed");
                    break;
                }
                const auto before = std::max(submitted_device_frames.load(std::memory_order_acquire),
                                             consumed_device_frames.load(std::memory_order_acquire));
                submitted_device_frames.store(before + requested_frames, std::memory_order_release);
                const auto audio_before = submitted_audio_frames.fetch_add(copied_frames, std::memory_order_acq_rel);
                if (!record_submission(before, requested_frames, copied_frames, audio_before)) {
                    set_terminal(Terminal::audio_error, "WASAPI submission accounting overflowed");
                    break;
                }
                if (end_of_stream.load(std::memory_order_acquire) && ring.available() == 0) {
                    end_device_frame.store(before + copied_frames, std::memory_order_release);
                }
                wake.notify_all();
            }
            {
                std::lock_guard lock(wake_mutex);
                render_exited.store(true, std::memory_order_release);
            }
            wake.notify_all();
            if (mmcss) AvRevertMmThreadCharacteristics(mmcss);
            CoUninitialize();
        }

        void monitor_loop() noexcept {
            for (;;) {
                std::unique_lock lock(wake_mutex);
                if (wake.wait_for(lock, std::chrono::seconds(1), [&] {
                        return stopping.load(std::memory_order_acquire) ||
                               terminal.load(std::memory_order_acquire) != Terminal::none;
                    })) {
                    lock.unlock();
                    if (!stopping.load(std::memory_order_acquire) &&
                        terminal.load(std::memory_order_acquire) != Terminal::none &&
                        spec.source.cancel) {
                        try {
                            spec.source.cancel();
                        } catch (...) {
                        }
                    }
                    break;
                }
                lock.unlock();
                if (playing.load(std::memory_order_acquire)) parent.timeupdate(token);
            }
        }

        void decode_loop() noexcept {
            CoInitializeEx(nullptr, COINIT_MULTITHREADED);
            try {
                const auto source_bpf =
                    static_cast<std::size_t>(source_format.channels * source_format.container_bits / 8);
                std::vector<std::uint8_t> decoded(source_bpf * 2048);
                while (!stopping.load(std::memory_order_acquire) &&
                       terminal.load(std::memory_order_acquire) == Terminal::none) {
                    if (quiesce_for_seek(decode_quiescent)) continue;
                    if (end_of_stream.load(std::memory_order_acquire)) {
                        std::unique_lock lock(wake_mutex);
                        wake.wait(lock, [&] {
                            return stopping.load(std::memory_order_acquire) ||
                                   terminal.load(std::memory_order_acquire) != Terminal::none ||
                                   seek_requested;
                        });
                        continue;
                    }
                    if (ring.free_space() < output_bytes_per_frame * 2048) {
                        std::unique_lock lock(wake_mutex);
                        wake.wait_for(lock, std::chrono::milliseconds(50), [&] {
                            return stopping.load(std::memory_order_acquire) ||
                                   terminal.load(std::memory_order_acquire) != Terminal::none ||
                                   seek_requested;
                        });
                        continue;
                    }
                    std::size_t frames = 0;
                    try {
                        std::lock_guard lock(decoder_mutex);
                        frames = decoder->read(decoded.data(), 2048);
                    } catch (const SourceError&) {
                        if (seek_pending()) continue;
                        throw;
                    }
                    if (seek_pending()) continue;
                    if (frames == 0) {
                        if (converter) {
                            const auto converted = converter->flush();
                            std::size_t written = 0;
                            while (written < converted.second &&
                                   !stopping.load(std::memory_order_acquire) &&
                                   terminal.load(std::memory_order_acquire) == Terminal::none &&
                                   !seek_pending()) {
                                written += ring.write(converted.first + written, converted.second - written);
                                if (written < converted.second) {
                                    std::unique_lock lock(wake_mutex);
                                    wake.wait_for(lock, std::chrono::milliseconds(50), [&] {
                                        return stopping.load(std::memory_order_acquire) ||
                                               terminal.load(std::memory_order_acquire) != Terminal::none ||
                                               seek_requested;
                                    });
                                }
                            }
                        }
                        if (!seek_pending()) end_of_stream.store(true, std::memory_order_release);
                        wake.notify_all();
                        continue;
                    }
                    const auto current_gain = gain.load(std::memory_order_acquire);
                    if (current_gain != 1.0) {
                        pcm::apply_gain(decoded.data(), frames, source_format, current_gain);
                        samples_changed.store(true, std::memory_order_release);
                    }
                    const std::uint8_t* output = decoded.data();
                    std::size_t bytes = frames * source_bpf;
                    if (converter) {
                        const auto converted = converter->convert(decoded.data(), frames);
                        output = converted.first;
                        bytes = converted.second;
                    }
                    std::size_t written = 0;
                    while (written < bytes && !stopping.load(std::memory_order_acquire) &&
                           terminal.load(std::memory_order_acquire) == Terminal::none &&
                           !seek_pending()) {
                        written += ring.write(output + written, bytes - written);
                        if (written < bytes) {
                            std::unique_lock lock(wake_mutex);
                            wake.wait_for(lock, std::chrono::milliseconds(50), [&] {
                                return stopping.load(std::memory_order_acquire) ||
                                       terminal.load(std::memory_order_acquire) != Terminal::none ||
                                       seek_requested;
                            });
                        }
                    }
                }
            } catch (const SourceError&) {
                if (!stopping.load(std::memory_order_acquire))
                    set_terminal(Terminal::media_failure, "Media source read failed");
            } catch (const DecodeError&) {
                if (!stopping.load(std::memory_order_acquire))
                    set_terminal(Terminal::media_failure, "Media decoding failed");
            } catch (const EngineError&) {
                if (!stopping.load(std::memory_order_acquire))
                    set_terminal(Terminal::audio_error, "PCM conversion failed");
            } catch (...) {
                if (!stopping.load(std::memory_order_acquire))
                    set_terminal(Terminal::media_failure, "Media decoding failed");
            }

            const auto outcome = terminal.load(std::memory_order_acquire);
            if (outcome != Terminal::none && !stopping.load(std::memory_order_acquire)) {
                {
                    std::unique_lock lock(wake_mutex);
                    wake.wait(lock, [&] { return render_exited.load(std::memory_order_acquire); });
                }
                release_endpoint();
                parent.terminal_event(token, outcome, terminal_message.load(std::memory_order_acquire));
            }
            CoUninitialize();
        }

        std::mutex resource_mutex;
        std::mutex decoder_mutex;
        std::mutex wake_mutex;
        std::condition_variable wake;
        std::thread render_thread;
        std::thread decode_thread;
        std::thread monitor_thread;
        std::atomic<bool> stopping{false};
        std::atomic<bool> playing{false};
        std::atomic<bool> end_of_stream{false};
        std::atomic<bool> render_exited{false};
        std::atomic<Terminal> terminal{Terminal::none};
        std::atomic<const char*> terminal_message{""};
        bool seek_requested = false;
        bool decode_quiescent = false;
        bool render_quiescent = false;
        std::atomic<std::uint64_t> consumed_device_frames{0};
        std::atomic<std::uint64_t> consumed_audio_frames{0};
        std::atomic<std::uint64_t> submitted_device_frames{0};
        std::atomic<std::uint64_t> submitted_audio_frames{0};
        std::atomic<std::uint64_t> end_device_frame{std::numeric_limits<std::uint64_t>::max()};
        std::array<Submission, 1024> submissions{};
        std::atomic<std::uint64_t> submission_read{0};
        std::atomic<std::uint64_t> submission_write{0};
        std::atomic_flag clock_update = ATOMIC_FLAG_INIT;
        std::atomic<std::uint64_t> clock_origin{0};
        std::atomic<std::uint64_t> clock_segment_base{0};
        std::atomic<bool> clock_running{false};
        std::uint64_t clock_frequency = 0;
        std::atomic<std::int64_t> base_position_ms{0};
    };

    nlohmann::json snapshot_locked() const {
        nlohmann::json result{{"state", state}, {"play_id", play_id}, {"sequence", sequence}, {"volume", volume}};
        if (!session || (state == "stopped" || state == "error")) {
            result["actual"] = nullptr;
            return result;
        }
        result["actual"] = {
            {"device_id", session->endpoint.id},
            {"name", session->endpoint.name},
            {"mode", session->spec.exclusive ? "exclusive" : "shared"},
            {"sample_rate", session->endpoint.output_format.sample_rate},
            {"channels", session->endpoint.output_format.channels},
            {"container_bits", session->endpoint.output_format.container_bits},
            {"valid_bits", session->endpoint.output_format.valid_bits},
            {"bit_transparent", session->transparent()},
            {"reason", session->transparency_reason()},
        };
        return result;
    }

    void require_media_locked(const std::string& expected) const {
        if (!session || play_id.empty()) throw EngineError("action_failed", "No media is loaded");
        if (expected != play_id) throw EngineError("action_failed", "Media identity changed");
    }

    void require_transport_locked(const std::string& expected, std::uint64_t requested_sequence) {
        require_media_locked(expected);
        if (requested_sequence < sequence)
            throw EngineError("action_failed", "Command sequence is stale");
        sequence = requested_sequence;
    }

    void timeupdate(std::uint64_t expected_token) noexcept {
        nlohmann::json event;
        try {
            {
                std::lock_guard lock(mutex);
                if (!session || session->token != expected_token || state != "playing") return;
                event = {
                    {"event", "observation"},
                    {"play_id", play_id},
                    {"sequence", sequence},
                    {"observation", observation("timeupdate", play_id, "playing", session->position_ms(),
                                                session->duration, true)},
                    {"audio", snapshot_locked()},
                };
            }
            emit(std::move(event));
        } catch (...) {
        }
    }

    void terminal_event(std::uint64_t expected_token, Terminal outcome, const char* message) noexcept {
        nlohmann::json event;
        try {
            {
                std::lock_guard lock(mutex);
                if (!session || session->token != expected_token) return;
                const auto position = session->position_ms();
                const auto duration = session->duration;
                const auto old_play_id = play_id;
                const bool ended = outcome == Terminal::ended;
                state = ended ? "stopped" : "error";
                const auto audio = snapshot_locked();
                event = {{"event", "observation"}, {"play_id", old_play_id}, {"sequence", sequence},
                         {"observation", observation(ended ? "ended" : "error", old_play_id, "", position, duration, true)},
                         {"audio", audio}};
                if (!ended) {
                    const char* code = outcome == Terminal::media_failure ? "media_failed" :
                                       outcome == Terminal::device_lost ? "device_lost" : "audio_error";
                    event["error"] = {{"code", code}, {"message", message}};
                }
            }
            emit(std::move(event));
        } catch (...) {
            // The process-level protocol owner handles a closed stdout by shutting down.
        }
    }

    void shutdown() noexcept {
        std::shared_ptr<Session> old;
        {
            std::lock_guard lock(mutex);
            old = std::move(session);
            state = "stopped";
            play_id.clear();
        }
        if (old) old->stop_and_join();
    }

    Emit emit;
    mutable std::mutex mutex;
    std::shared_ptr<Session> session;
    std::string state = "stopped";
    std::string play_id;
    std::uint64_t sequence = 0;
    double volume = 1.0;
    std::uint64_t next_token = 1;
};

std::vector<EndpointInfo> AudioEngine::enumerate_endpoints() {
    ComPtr<IMMDeviceEnumerator> enumerator;
    if (FAILED(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL,
                                IID_PPV_ARGS(&enumerator)))) return {};
    ComPtr<IMMDevice> default_device;
    std::string default_id;
    if (SUCCEEDED(enumerator->GetDefaultAudioEndpoint(eRender, eMultimedia, &default_device))) {
        LPWSTR value = nullptr;
        if (SUCCEEDED(default_device->GetId(&value))) {
            default_id = utf8(value);
            CoTaskMemFree(value);
        }
    }
    ComPtr<IMMDeviceCollection> collection;
    if (FAILED(enumerator->EnumAudioEndpoints(eRender, DEVICE_STATE_ACTIVE, &collection))) return {};
    UINT count = 0;
    collection->GetCount(&count);
    std::vector<EndpointInfo> result;
    result.reserve(count);
    for (UINT index = 0; index < count; ++index) {
        ComPtr<IMMDevice> device;
        if (FAILED(collection->Item(index, &device))) continue;
        LPWSTR value = nullptr;
        if (FAILED(device->GetId(&value))) continue;
        auto id = utf8(value);
        CoTaskMemFree(value);
        result.push_back({id, endpoint_name(device.Get()), id == default_id});
    }
    return result;
}
AudioEngine::AudioEngine(Emit emit) : impl_(std::make_unique<Impl>(std::move(emit))) {}
AudioEngine::~AudioEngine() = default;

nlohmann::json AudioEngine::set_uri(MediaSpec spec) {
    if (spec.play_id.empty() || spec.sequence == 0 || !spec.source.read ||
        !spec.source.cancel || !spec.source.rearm)
        throw EngineError("invalid_argument", "set_uri requires media identity and source callbacks");
    if (!std::isfinite(spec.volume) || spec.volume < 0.0 || spec.volume > 1.0)
        throw EngineError("invalid_argument", "Volume must be between zero and one");
    std::shared_ptr<Impl::Session> old;
    {
        std::lock_guard lock(impl_->mutex);
        if (impl_->state != "stopped" && impl_->state != "error")
            throw EngineError("stop_required", "Stop native audio before loading another media item");
        old = std::move(impl_->session);
    }
    if (old) old->stop_and_join();
    std::shared_ptr<Impl::Session> candidate;
    try {
        candidate = std::make_shared<Impl::Session>(*impl_, impl_->next_token++, std::move(spec));
    } catch (const SourceError&) {
        throw EngineError("media_failed", "Media source could not be read");
    } catch (const DecodeError&) {
        throw EngineError("media_failed", "Media could not be decoded");
    }
    {
        std::lock_guard lock(impl_->mutex);
        impl_->session = candidate;
        impl_->play_id = candidate->spec.play_id;
        impl_->sequence = candidate->spec.sequence;
        impl_->volume = candidate->spec.volume;
        impl_->state = "loaded";
    }
    try {
        candidate->start_threads();
    } catch (...) {
        std::lock_guard lock(impl_->mutex);
        if (impl_->session == candidate) impl_->session.reset();
        impl_->state = "error";
        throw;
    }
    std::lock_guard lock(impl_->mutex);
    return {{"observation", observation("loaded", impl_->play_id, "paused", 0, candidate->duration, true)},
            {"audio", impl_->snapshot_locked()}};
}

nlohmann::json AudioEngine::play(const std::string& play_id, std::uint64_t sequence) {
    std::shared_ptr<Impl::Session> current;
    {
        std::lock_guard lock(impl_->mutex);
        impl_->require_transport_locked(play_id, sequence);
        current = impl_->session;
    }
    try {
        current->start_playback();
    } catch (const EngineError& error) {
        if (error.code() == "device_lost" || error.code() == "audio_error") {
            current->stop_and_join();
            std::lock_guard lock(impl_->mutex);
            if (impl_->session == current) impl_->state = "error";
        }
        throw;
    }
    std::lock_guard lock(impl_->mutex);
    impl_->require_media_locked(play_id);
    impl_->state = "playing";
    return {{"observation", observation("playing", play_id, "playing", current->position_ms(), current->duration, true)},
            {"audio", impl_->snapshot_locked()}};
}

nlohmann::json AudioEngine::pause(const std::string& play_id, std::uint64_t sequence) {
    std::shared_ptr<Impl::Session> current;
    {
        std::lock_guard lock(impl_->mutex);
        impl_->require_transport_locked(play_id, sequence);
        current = impl_->session;
    }
    try {
        current->pause_playback();
    } catch (const EngineError& error) {
        if (error.code() == "device_lost" || error.code() == "audio_error") {
            current->stop_and_join();
            std::lock_guard lock(impl_->mutex);
            if (impl_->session == current) impl_->state = "error";
        }
        throw;
    }
    std::lock_guard lock(impl_->mutex);
    impl_->require_media_locked(play_id);
    impl_->state = "paused";
    return {{"observation", observation("pause", play_id, "paused", current->position_ms(), current->duration, true)},
            {"audio", impl_->snapshot_locked()}};
}

nlohmann::json AudioEngine::seek(const std::string& play_id, std::uint64_t sequence, std::int64_t position_ms) {
    std::shared_ptr<Impl::Session> current;
    std::string wanted_state;
    {
        std::lock_guard lock(impl_->mutex);
        impl_->require_transport_locked(play_id, sequence);
        current = impl_->session;
        wanted_state = impl_->state == "playing" ? "playing" : "paused";
    }
    std::int64_t actual_position = 0;
    try {
        actual_position = current->seek_to(position_ms);
    } catch (const EngineError& error) {
        if (error.code() == "device_lost" || error.code() == "audio_error" || error.code() == "media_error") {
            current->stop_and_join();
            std::lock_guard lock(impl_->mutex);
            if (impl_->session == current) impl_->state = "error";
        }
        throw;
    }
    std::lock_guard lock(impl_->mutex);
    impl_->require_media_locked(play_id);
    impl_->state = wanted_state;
    return {{"observation", observation("seeked", play_id, wanted_state, actual_position,
                                        current->duration, true)},
            {"audio", impl_->snapshot_locked()}};
}
nlohmann::json AudioEngine::stop(const std::string& requested_play_id, std::uint64_t sequence) {
    std::shared_ptr<Impl::Session> old;
    std::string observed_play_id;
    std::int64_t position = 0;
    std::int64_t duration = 0;
    {
        std::lock_guard lock(impl_->mutex);
        if (!impl_->play_id.empty() && requested_play_id != impl_->play_id)
            throw EngineError("action_failed", "Media identity changed");
        if (sequence < impl_->sequence)
            throw EngineError("action_failed", "Command sequence is stale");
        impl_->sequence = sequence;
        old = std::move(impl_->session);
        observed_play_id = impl_->play_id.empty() ? requested_play_id : impl_->play_id;
        if (old) duration = old->duration;
    }
    if (old) {
        old->stop_and_join();
        position = old->position_ms();
    }
    std::lock_guard lock(impl_->mutex);
    impl_->play_id.clear();
    impl_->state = "stopped";
    return {{"observation", observation("stopped", observed_play_id, "paused", position, duration, old != nullptr)},
            {"audio", impl_->snapshot_locked()}};
}

nlohmann::json AudioEngine::set_volume(double volume) {
    if (!std::isfinite(volume) || volume < 0.0 || volume > 1.0)
        throw EngineError("invalid_argument", "Volume must be between zero and one");
    std::lock_guard lock(impl_->mutex);
    impl_->volume = volume;
    if (impl_->session) impl_->session->set_gain(volume);
    return impl_->snapshot_locked();
}

nlohmann::json AudioEngine::status() const {
    std::lock_guard lock(impl_->mutex);
    return impl_->snapshot_locked();
}

void AudioEngine::shutdown() { impl_->shutdown(); }

#else

class AudioEngine::Impl {};

std::vector<EndpointInfo> AudioEngine::enumerate_endpoints() { return {}; }
AudioEngine::AudioEngine(Emit) : impl_(std::make_unique<Impl>()) {}
AudioEngine::~AudioEngine() = default;
nlohmann::json AudioEngine::set_uri(MediaSpec) { throw EngineError("unsupported_platform", "Native audio requires Windows"); }
nlohmann::json AudioEngine::play(const std::string&, std::uint64_t) { throw EngineError("unsupported_platform", "Native audio requires Windows"); }
nlohmann::json AudioEngine::pause(const std::string&, std::uint64_t) { throw EngineError("unsupported_platform", "Native audio requires Windows"); }
nlohmann::json AudioEngine::stop(const std::string&, std::uint64_t) { throw EngineError("unsupported_platform", "Native audio requires Windows"); }
nlohmann::json AudioEngine::seek(const std::string&, std::uint64_t, std::int64_t) { throw EngineError("unsupported_platform", "Native audio requires Windows"); }
nlohmann::json AudioEngine::set_volume(double) { throw EngineError("unsupported_platform", "Native audio requires Windows"); }
nlohmann::json AudioEngine::status() const { return {{"state", "stopped"}, {"play_id", ""}, {"sequence", 0}, {"volume", 1.0}, {"actual", nullptr}}; }
void AudioEngine::shutdown() {}

#endif

} // namespace jastreamer
