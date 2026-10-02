#include "audio_engine.hpp"
#include "coreaudio_pcm.hpp"
#include "coreaudio_format.hpp"

#include <CoreAudio/CoreAudio.h>
#include <CoreFoundation/CoreFoundation.h>
#include <mach/mach_time.h>
#include <unistd.h>

#include <algorithm>
#include <atomic>
#include <chrono>
#include <cmath>
#include <cstring>
#include <limits>
#include <mutex>
#include <thread>
#include <utility>

extern "C" {
#include <libavutil/channel_layout.h>
#include <libavutil/samplefmt.h>
#include <libswresample/swresample.h>
}

namespace jastreamer {
namespace {
using namespace std::chrono_literals;
static_assert(std::atomic<std::uint64_t>::is_always_lock_free);
static_assert(std::atomic<std::size_t>::is_always_lock_free);
static_assert(std::atomic<bool>::is_always_lock_free);

AudioObjectPropertyAddress address(AudioObjectPropertySelector selector,
                                   AudioObjectPropertyScope scope = kAudioObjectPropertyScopeGlobal,
                                   AudioObjectPropertyElement element = kAudioObjectPropertyElementMain) {
    return {selector, scope, element};
}
template<class T>
bool get(AudioObjectID object, AudioObjectPropertyAddress property, T& value) noexcept {
    UInt32 size = sizeof(value);
    return AudioObjectGetPropertyData(object, &property, 0, nullptr, &size, &value) == noErr && size == sizeof(value);
}
template<class T>
T required(AudioObjectID object, AudioObjectPropertyAddress property) {
    T value{};
    if (!get(object, property, value)) throw EngineError("device_unavailable", "Cannot read CoreAudio device property");
    return value;
}
template<class T>
void set(AudioObjectID object, AudioObjectPropertyAddress property, const T& value, const char* code) {
    Boolean writable = false;
    if (AudioObjectIsPropertySettable(object, &property, &writable) != noErr || !writable ||
        AudioObjectSetPropertyData(object, &property, 0, nullptr, sizeof(value), &value) != noErr)
        throw EngineError(code, "CoreAudio rejected the requested device setting");
}
template<class T>
std::vector<T> list(AudioObjectID object, AudioObjectPropertyAddress property) {
    UInt32 bytes = 0;
    if (AudioObjectGetPropertyDataSize(object, &property, 0, nullptr, &bytes) != noErr || bytes % sizeof(T))
        throw EngineError("device_unavailable", "Cannot inspect CoreAudio device capabilities");
    std::vector<T> result(bytes / sizeof(T));
    if (bytes && AudioObjectGetPropertyData(object, &property, 0, nullptr, &bytes, result.data()) != noErr)
        throw EngineError("device_unavailable", "CoreAudio device disappeared during enumeration");
    result.resize(bytes / sizeof(T));
    return result;
}
std::string text(AudioObjectID object, AudioObjectPropertySelector selector) {
    CFStringRef value = nullptr;
    if (!get(object, address(selector), value) || !value) return {};
    const auto length = CFStringGetMaximumSizeForEncoding(CFStringGetLength(value), kCFStringEncodingUTF8) + 1;
    std::string result(static_cast<std::size_t>(length), '\0');
    const bool ok = CFStringGetCString(value, result.data(), length, kCFStringEncodingUTF8);
    CFRelease(value);
    if (!ok) return {};
    result.resize(std::strlen(result.c_str()));
    return result;
}
using coreaudio::same;
using coreaudio::usable;
AudioFormat format(const AudioStreamBasicDescription& f) {
    return {static_cast<int>(f.mSampleRate), static_cast<int>(f.mChannelsPerFrame),
            static_cast<int>(f.mBytesPerFrame / f.mChannelsPerFrame * 8), static_cast<int>(f.mBitsPerChannel),
            (f.mFormatFlags & kAudioFormatFlagIsFloat) != 0, pcm::default_channel_mask(f.mChannelsPerFrame), false};
}
void establish_physical_format(AudioDeviceID device, AudioStreamID stream,
                               const AudioStreamBasicDescription& wanted, bool change_physical) {
    if (change_physical)
        set(stream, address(kAudioStreamPropertyPhysicalFormat), wanted, "exclusive_unsupported");
    // A physical change asynchronously drives both HAL formats and the nominal
    // rate. Setting VirtualFormat here can reset the physical stream to mixable
    // float defaults even after an apparently successful immediate readback.
    // Wait for the complete tuple; never manufacture agreement with another set.
    for (int attempt = 0; attempt != 200; ++attempt) {
        AudioStreamBasicDescription physical{}, virtual_format{};
        Float64 rate = 0;
        pid_t owner = -1;
        if (get(stream, address(kAudioStreamPropertyPhysicalFormat), physical) && same(physical, wanted) &&
            get(stream, address(kAudioStreamPropertyVirtualFormat), virtual_format) && same(virtual_format, wanted) &&
            get(device, address(kAudioDevicePropertyNominalSampleRate), rate) && rate == wanted.mSampleRate &&
            get(device, address(kAudioDevicePropertyHogMode), owner) && owner == getpid()) return;
        std::this_thread::sleep_for(10ms);
    }
    throw EngineError("exclusive_unsupported", "CoreAudio did not establish exact physical/virtual PCM, rate and Hog ownership");
}

struct IntegrityIssue {
    enum class Property { none, alive, virtual_format, hog, rate, physical_format, mixing };
    Property property = Property::none;
    bool readable = false;
    AudioStreamBasicDescription observed_format{};
    double observed_number = 0;
    explicit operator bool() const noexcept { return property != Property::none; }
    std::string message(const AudioStreamBasicDescription& expected_format) const {
        const char* name = property == Property::alive ? "device alive" :
            property == Property::virtual_format ? "virtual format" :
            property == Property::hog ? "Hog owner" :
            property == Property::rate ? "nominal rate" :
            property == Property::physical_format ? "physical format" : "mixing";
        if (!readable) return std::string("CoreAudio ") + name + " could not be read; no fallback was selected";
        if (property == Property::virtual_format || property == Property::physical_format) {
            const auto describe = [](const AudioStreamBasicDescription& f) {
                return nlohmann::json{{"rate", f.mSampleRate}, {"format", f.mFormatID}, {"flags", f.mFormatFlags},
                    {"packet_bytes", f.mBytesPerPacket}, {"packet_frames", f.mFramesPerPacket},
                    {"frame_bytes", f.mBytesPerFrame}, {"channels", f.mChannelsPerFrame}, {"bits", f.mBitsPerChannel}}.dump();
            };
            return std::string("CoreAudio ") + name + " changed: expected " + describe(expected_format) +
                   ", observed " + describe(observed_format) + "; no fallback was selected";
        }
        const double expected = property == Property::alive ? 1 :
            property == Property::hog ? getpid() : property == Property::rate ? expected_format.mSampleRate : 0;
        return std::string("CoreAudio ") + name + " changed: expected " + std::to_string(expected) +
               ", observed " + std::to_string(observed_number) + "; no fallback was selected";
    }
};

class Endpoint final {
public:
    Endpoint(const MediaSpec& spec, const AudioFormat& source, AudioDeviceIOProc callback, void* context) {
        try { open(spec, source, callback, context); } catch (...) { release(); throw; }
    }
    ~Endpoint() { release(); }
    Endpoint(const Endpoint&) = delete;
    Endpoint& operator=(const Endpoint&) = delete;

    void open(const MediaSpec& spec, const AudioFormat& source, AudioDeviceIOProc callback, void* context) {
        AudioDeviceID default_device = kAudioObjectUnknown;
        get(kAudioObjectSystemObject, address(kAudioHardwarePropertyDefaultOutputDevice), default_device);
        for (auto candidate : list<AudioDeviceID>(kAudioObjectSystemObject, address(kAudioHardwarePropertyDevices))) {
            if ((spec.device_id == "default" && candidate == default_device) ||
                (spec.device_id != "default" && text(candidate, kAudioDevicePropertyDeviceUID) == spec.device_id)) {
                device = candidate;
                break;
            }
        }
        if (!device) throw EngineError("device_not_found", "Requested CoreAudio output device is unavailable");
        UInt32 alive = 0;
        if (!get(device, address(kAudioDevicePropertyDeviceIsAlive), alive) || !alive)
            throw EngineError("device_not_found", "Requested CoreAudio output device is disconnected");
        id = text(device, kAudioDevicePropertyDeviceUID);
        name = text(device, kAudioObjectPropertyName);
        const auto streams = list<AudioStreamID>(device, address(kAudioDevicePropertyStreams, kAudioObjectPropertyScopeOutput));
        if (streams.empty()) throw EngineError("device_unavailable", "CoreAudio device has no output streams");
        // Multi-stream interfaces have separate HAL buffers. Select a complete
        // interleaved stream, never silently remap exclusive source channels.
        std::size_t buffer_offset = 0;
        for (std::size_t index = 0; index != streams.size(); ++index) {
            auto current = required<AudioStreamBasicDescription>(streams[index], address(kAudioStreamPropertyVirtualFormat));
            const auto current_buffer = buffer_offset;
            buffer_offset += (current.mFormatFlags & kAudioFormatFlagIsNonInterleaved) ? current.mChannelsPerFrame : 1;
            if (!usable(current)) continue;
            if (spec.exclusive && current.mChannelsPerFrame != static_cast<UInt32>(source.channels)) continue;
            stream = streams[index];
            buffer_index = current_buffer;
            original_virtual = current;
            break;
        }
        if (!stream) throw EngineError(spec.exclusive ? "exclusive_unsupported" : "media_unsupported",
                                        "No compatible interleaved CoreAudio output stream");
        original_physical = required<AudioStreamBasicDescription>(stream, address(kAudioStreamPropertyPhysicalFormat));
        original_rate = required<Float64>(device, address(kAudioDevicePropertyNominalSampleRate));
        if (spec.exclusive) {
            if (source.channels > 2 || (source.channel_mask && source.channel_mask != pcm::default_channel_mask(source.channels)))
                throw EngineError("exclusive_unsupported", "Exact CoreAudio channel mapping is verified only for mono and stereo");
            auto hog = required<pid_t>(device, address(kAudioDevicePropertyHogMode));
            if (hog != -1 && hog != getpid()) throw EngineError("exclusive_busy", "CoreAudio device is owned by another process");
            hog_owned = true; // Include partial-setting failure in rollback.
            set(device, address(kAudioDevicePropertyHogMode), getpid(), "exclusive_busy");
            if (required<pid_t>(device, address(kAudioDevicePropertyHogMode)) != getpid())
                throw EngineError("exclusive_busy", "CoreAudio did not grant exclusive Hog ownership");
        }
        // Bind the callback lifetime to this endpoint before configuration.
        // Registration alone does not start IO or consume any media.
        if (AudioDeviceCreateIOProcID(device, callback, context, &io) != noErr)
            throw EngineError("audio_error", "Cannot register CoreAudio output callback");
        if (spec.exclusive) {
            const auto mix = address(kAudioDevicePropertySupportsMixing, kAudioObjectPropertyScopeOutput);
            mix_property = mix;
            if (!AudioObjectHasProperty(device, &mix_property))
                mix_property = address(kAudioDevicePropertySupportsMixing);
            if (AudioObjectHasProperty(device, &mix_property)) {
                mixing_control = true;
                original_mixing = required<UInt32>(device, mix_property);
                if (original_mixing) {
                    mixing_changed = true;
                    set(device, mix_property, UInt32{0}, "exclusive_unsupported");
                }
                mixing_disabled = required<UInt32>(device, mix_property) == 0;
                if (!mixing_disabled) throw EngineError("exclusive_unsupported", "CoreAudio device mixing could not be disabled");
            }
            const auto physical_formats = list<AudioStreamRangedDescription>(stream, address(kAudioStreamPropertyAvailablePhysicalFormats));
            const auto virtual_formats = list<AudioStreamRangedDescription>(stream, address(kAudioStreamPropertyAvailableVirtualFormats));
            const auto selection = coreaudio::exclusive_format(source, physical_formats, virtual_formats);
            if (!selection) throw EngineError("exclusive_unsupported", "DAC has no lossless exact physical/virtual PCM format for this source");
            const auto selected = *selection;
            rate_changed = original_rate != source.sample_rate;
            physical_changed = !same(original_physical, selected);
            virtual_changed = !same(original_virtual, selected);
            establish_physical_format(device, stream, selected, physical_changed);
            actual = selected;
            // The HAL nonmixable format flag is independently verifiable mixing
            // exclusion, including modern USB drivers without the legacy control.
            if (actual.mFormatFlags & kAudioFormatFlagIsNonMixable) mixing_disabled = true;
        } else {
            actual = required<AudioStreamBasicDescription>(stream, address(kAudioStreamPropertyVirtualFormat));
            if (!usable(actual)) throw EngineError("media_unsupported", "Registered CoreAudio client has an unsupported PCM format");
        }
        output = format(actual);
        starting_channel_known = get(stream, address(kAudioStreamPropertyStartingChannel), starting_channel) &&
                                 starting_channel > 0 && starting_channel <= std::numeric_limits<UInt32>::max() - actual.mChannelsPerFrame;
        hardware_unity = unity_controls();
        UInt32 latency = 0, stream_latency = 0, safety = 0;
        get(device, address(kAudioDevicePropertyLatency, kAudioObjectPropertyScopeOutput), latency);
        get(stream, address(kAudioStreamPropertyLatency), stream_latency);
        get(device, address(kAudioDevicePropertySafetyOffset, kAudioObjectPropertyScopeOutput), safety);
        latency_frames = static_cast<std::uint64_t>(latency) + stream_latency + safety;
        if (latency_frames > static_cast<std::uint64_t>(output.sample_rate) * 2)
            throw EngineError("audio_error", "CoreAudio reported an unreasonable output latency");
    }
    bool unity_controls() const noexcept {
        // Do not alter user volume/mute. Exposed attenuation prevents a
        // transparency claim even though the application's samples are exact.
        if (!starting_channel_known) return false;
        for (UInt32 index = 0; index <= actual.mChannelsPerFrame; ++index) {
            const UInt32 channel = index == 0 ? kAudioObjectPropertyElementMain : starting_channel + index - 1;
            auto volume = address(kAudioDevicePropertyVolumeScalar, kAudioObjectPropertyScopeOutput, channel);
            if (AudioObjectHasProperty(device, &volume)) {
                Float32 gain = 0;
                if (!get(device, volume, gain) || gain != 1.0f) return false;
            }
            auto mute = address(kAudioDevicePropertyMute, kAudioObjectPropertyScopeOutput, channel);
            if (AudioObjectHasProperty(device, &mute)) {
                UInt32 muted = 0;
                if (!get(device, mute, muted) || muted) return false;
            }
        }
        return true;
    }
    IntegrityIssue inspect(bool exclusive) const noexcept {
        using Property = IntegrityIssue::Property;
        UInt32 alive = 0;
        bool readable = get(device, address(kAudioDevicePropertyDeviceIsAlive), alive);
        if (!readable || !alive) return {Property::alive, readable, {}, static_cast<double>(alive)};
        AudioStreamBasicDescription current{};
        readable = get(stream, address(kAudioStreamPropertyVirtualFormat), current);
        if (!readable || !same(current, actual)) return {Property::virtual_format, readable, current};
        if (exclusive) {
            pid_t hog = -1;
            readable = get(device, address(kAudioDevicePropertyHogMode), hog);
            if (!readable || hog != getpid()) return {Property::hog, readable, {}, static_cast<double>(hog)};
            Float64 rate = 0;
            readable = get(device, address(kAudioDevicePropertyNominalSampleRate), rate);
            if (!readable || rate != actual.mSampleRate) return {Property::rate, readable, {}, rate};
            readable = get(stream, address(kAudioStreamPropertyPhysicalFormat), current);
            if (!readable || !same(current, actual)) return {Property::physical_format, readable, current};
            if (mixing_control) {
                UInt32 mixing = 0;
                readable = get(device, mix_property, mixing);
                if (!readable || mixing) return {Property::mixing, readable, {}, static_cast<double>(mixing)};
            }
        }
        return {};
    }
    void stop_io() noexcept {
        if (io && started) AudioDeviceStop(device, io);
        started = false;
    }
    void start_io() {
        if (released || !io) throw EngineError("audio_error", "CoreAudio session is no longer active");
        if (started) return;
        if (AudioDeviceStart(device, io) != noErr) throw EngineError("device_lost", "CoreAudio output could not start");
        started = true;
    }
    bool release() noexcept {
        if (released) return restore_ok;
        stop_io();
        const auto destroy_io = [&] {
            if (io && AudioDeviceDestroyIOProcID(device, io) != noErr) restore_ok = false;
            io = nullptr;
        };
        if (hog_owned) {
            pid_t owner = -1;
            if (!get(device, address(kAudioDevicePropertyHogMode), owner) || owner != getpid()) {
                // The device is gone or another owner has taken over. Do not
                // overwrite that owner's settings during error cleanup.
                destroy_io();
                released = true;
                restore_ok = false;
                return false;
            }
        }
        // Restore the physical-driven tuple first. A VirtualFormat setter can
        // itself reset the physical stream, so use it only when a saved custom
        // virtual format did not follow the settled original physical format.
        const auto restore = [&](AudioObjectID object, AudioObjectPropertyAddress property, const auto& value) {
            if (AudioObjectSetPropertyData(object, &property, 0, nullptr, sizeof(value), &value) != noErr)
                restore_ok = false;
        };
        const auto settings_match = [&] {
            AudioStreamBasicDescription current{};
            Float64 rate = 0;
            UInt32 mixing = 0;
            return (!virtual_changed || (get(stream, address(kAudioStreamPropertyVirtualFormat), current) && same(current, original_virtual))) &&
                   (!physical_changed || (get(stream, address(kAudioStreamPropertyPhysicalFormat), current) && same(current, original_physical))) &&
                   (!rate_changed || (get(device, address(kAudioDevicePropertyNominalSampleRate), rate) && rate == original_rate)) &&
                   (!mixing_changed || (get(device, mix_property, mixing) && mixing == original_mixing));
        };
        const auto wait_settings = [&] {
            for (int attempt = 0; attempt != 200; ++attempt) {
                if (settings_match()) return true;
                std::this_thread::sleep_for(10ms);
            }
            return false;
        };
        if (mixing_changed) restore(device, mix_property, original_mixing);
        if (physical_changed) restore(stream, address(kAudioStreamPropertyPhysicalFormat), original_physical);
        else if (rate_changed) restore(device, address(kAudioDevicePropertyNominalSampleRate), original_rate);
        bool restored = wait_settings();
        if (!restored && virtual_changed) {
            AudioStreamBasicDescription physical{};
            Float64 rate = 0;
            // Do not overwrite an unsettled device with a second format request.
            if (get(stream, address(kAudioStreamPropertyPhysicalFormat), physical) && same(physical, original_physical) &&
                get(device, address(kAudioDevicePropertyNominalSampleRate), rate) && rate == original_rate) {
                restore(stream, address(kAudioStreamPropertyVirtualFormat), original_virtual);
                restored = wait_settings();
            }
        }
        if (!restored) restore_ok = false;
        destroy_io();
        if (!wait_settings()) restore_ok = false;
        if (hog_owned) {
            restore(device, address(kAudioDevicePropertyHogMode), pid_t{-1});
            bool ownership_released = false;
            for (int attempt = 0; attempt != 200; ++attempt) {
                pid_t owner = getpid();
                if (get(device, address(kAudioDevicePropertyHogMode), owner) && owner != getpid()) {
                    ownership_released = true;
                    break;
                }
                std::this_thread::sleep_for(10ms);
            }
            if (!ownership_released) restore_ok = false;
        }
        released = true;
        return restore_ok;
    }

    AudioDeviceID device = 0;
    AudioStreamID stream = 0;
    std::size_t buffer_index = 0;
    AudioDeviceIOProcID io = nullptr;
    AudioStreamBasicDescription actual{};
    AudioFormat output{};
    std::string id, name;
    std::uint64_t latency_frames = 0;
    bool mixing_disabled = false, hardware_unity = false;
    UInt32 starting_channel = 0;
    bool starting_channel_known = false;
private:
    AudioStreamBasicDescription original_physical{}, original_virtual{};
    Float64 original_rate = 0;
    AudioObjectPropertyAddress mix_property{};
    UInt32 original_mixing = 0;
    bool hog_owned = false, mixing_control = false, mixing_changed = false, physical_changed = false;
    bool virtual_changed = false, rate_changed = false, started = false, released = false, restore_ok = true;
};

AVSampleFormat sample_format(const AudioFormat& f) {
    if (f.floating_point) return f.container_bits == 32 ? AV_SAMPLE_FMT_FLT : AV_SAMPLE_FMT_DBL;
    switch (f.container_bits) {
    case 8: return AV_SAMPLE_FMT_U8;
    case 16: return AV_SAMPLE_FMT_S16;
    case 24: case 32: return AV_SAMPLE_FMT_S32;
    case 64: return AV_SAMPLE_FMT_S64;
    default: throw EngineError("media_unsupported", "Unsupported PCM conversion container");
    }
}
class SharedConverter final {
public:
    SharedConverter(const AudioFormat& input, const AudioFormat& output, bool high)
        : output_(output), high_(high) {
        AVChannelLayout in{}, out{};
        if (input.channel_mask) av_channel_layout_from_mask(&in, input.channel_mask);
        else av_channel_layout_default(&in, input.channels);
        av_channel_layout_default(&out, output.channels);
        const int result = swr_alloc_set_opts2(&context_, &out, sample_format(output), output.sample_rate,
                                              &in, sample_format(input), input.sample_rate, 0, nullptr);
        av_channel_layout_uninit(&in);
        av_channel_layout_uninit(&out);
        if (result < 0 || !context_ || swr_init(context_) < 0) {
            swr_free(&context_);
            throw EngineError("media_unsupported", "Cannot configure shared CoreAudio PCM conversion");
        }
    }
    ~SharedConverter() { swr_free(&context_); }
    std::pair<const std::uint8_t*, std::size_t> convert(const std::uint8_t* input, int frames) {
        const int capacity = std::max(256, swr_get_out_samples(context_, frames));
        const auto bytes = av_get_bytes_per_sample(sample_format(output_));
        data_.resize(static_cast<std::size_t>(capacity) * output_.channels * bytes);
        auto* destination = data_.data();
        const int count = swr_convert(context_, &destination, capacity, input ? &input : nullptr, frames);
        if (count < 0) throw EngineError("media_error", "Shared CoreAudio sample conversion failed");
        const auto samples = static_cast<std::size_t>(count) * output_.channels;
        if (!output_.floating_point) {
            packed_.resize(samples * (output_.container_bits / 8));
            coreaudio::pack_integer(data_.data(), packed_.data(), samples, bytes,
                                    output_.container_bits / 8, output_.valid_bits, output_.valid_bits, high_);
            return {packed_.data(), packed_.size()};
        }
        return {data_.data(), samples * bytes};
    }
private:
    SwrContext* context_ = nullptr;
    AudioFormat output_;
    bool high_;
    std::vector<std::uint8_t> data_, packed_;
};

nlohmann::json observation(const char* event, const std::string& play_id, const char* state,
                           std::int64_t position, std::int64_t duration, bool has_position = true) {
    nlohmann::json result{{"event", event}, {"play_id", play_id}, {"position_ms", position},
                          {"duration_ms", duration}, {"has_position", has_position}};
    if (*state) result["state"] = state;
    return result;
}
} // namespace

class AudioEngine::Impl final {
public:
    explicit Impl(Emit callback) : emit(std::move(callback)) {}
    ~Impl() { shutdown(); }
    enum class Failure { none, media, device, underrun, callback, restore };
    class Session final {
    public:
        Session(Impl& parent, MediaSpec spec)
            : parent(parent), spec(std::move(spec)),
              decoder(std::make_unique<Decoder>(this->spec.source.read, this->spec.size, this->spec.seekable)),
              source(decoder->format()), duration(decoder->duration_ms() > 0 ? decoder->duration_ms() : this->spec.declared_duration_ms),
              endpoint(this->spec, source, render, this), frame_bytes(endpoint.actual.mBytesPerFrame),
              ring(static_cast<std::size_t>(endpoint.output.sample_rate) * frame_bytes) {
            pcm::validate_exclusive_source_format(source);
            gain.store(this->spec.volume);
            hardware_unity.store(endpoint.hardware_unity);
            if (!this->spec.exclusive) converter = std::make_unique<SharedConverter>(source, endpoint.output, aligned_high());
            mach_timebase_info_data_t clock{};
            mach_timebase_info(&clock);
            ticks_per_second = 1.0e9 * clock.denom / clock.numer;
        }
        ~Session() { stop_and_join(); }
        bool aligned_high() const noexcept { return (endpoint.actual.mFormatFlags & kAudioFormatFlagIsAlignedHigh) != 0; }
        void start_workers() {
            try {
                decode_thread = std::thread([this] { decode_loop(); });
                monitor_thread = std::thread([this] { monitor_loop(); });
            } catch (...) {
                stop_and_join();
                throw EngineError("audio_error", "Cannot start CoreAudio worker threads");
            }
        }
        void push(const std::uint8_t* bytes, std::size_t count) {
            std::size_t offset = 0;
            while (offset < count && !decode_stop.load() && !stopping.load()) {
                offset += ring.push(bytes + offset, count - offset);
                if (offset < count) std::this_thread::sleep_for(2ms);
            }
        }
        void decode_loop() noexcept {
            try {
                std::vector<std::uint8_t> input(4096ULL * source.channels * (source.container_bits / 8));
                std::vector<std::uint8_t> packed(4096ULL * frame_bytes);
                while (!decode_stop.load() && !stopping.load()) {
                    // Decode no further than one block beyond the bounded ring.
                    if (ring.readable() >= static_cast<std::size_t>(endpoint.output.sample_rate) * frame_bytes * 3 / 4) {
                        std::this_thread::sleep_for(2ms);
                        continue;
                    }
                    const auto frames = decoder->read(input.data(), 4096);
                    if (decode_stop.load() || stopping.load()) break;
                    if (!frames) {
                        if (converter) {
                            for (;;) {
                                const auto [bytes, size] = converter->convert(nullptr, 0);
                                if (!size || decode_stop.load() || stopping.load()) break;
                                push(bytes, size);
                            }
                        }
                        eof.store(true, std::memory_order_release);
                        break;
                    }
                    if (converter) {
                        pcm::apply_gain(input.data(), frames, source, gain.load());
                        const auto [bytes, size] = converter->convert(input.data(), static_cast<int>(frames));
                        push(bytes, size);
                    } else if (source.floating_point) {
                        push(input.data(), frames * frame_bytes);
                    } else {
                        coreaudio::pack_integer(input.data(), packed.data(), frames * source.channels,
                                                source.container_bits / 8, endpoint.output.container_bits / 8,
                                                source.valid_bits, endpoint.output.valid_bits, aligned_high());
                        push(packed.data(), frames * frame_bytes);
                    }
                }
            } catch (...) {
                if (!decode_stop.load() && !stopping.load()) failure.store(Failure::media);
            }
        }
        static OSStatus render(AudioObjectID, const AudioTimeStamp* now, const AudioBufferList*,
                               const AudioTimeStamp*, AudioBufferList* output, const AudioTimeStamp* output_time,
                               void* context) noexcept {
            auto& self = *static_cast<Session*>(context);
            struct Cycle final {
                std::atomic<std::uint64_t>& version;
                explicit Cycle(std::atomic<std::uint64_t>& value) : version(value) { version.fetch_add(1); }
                ~Cycle() { version.fetch_add(1, std::memory_order_release); }
            } cycle(self.callback_version);
            // No CoreAudio property queries, FFmpeg calls, locks, allocation,
            // notifications, or blocking source reads are permitted here.
            if (!output) return noErr;
            for (UInt32 i = 0; i < output->mNumberBuffers; ++i)
                if (output->mBuffers[i].mData) std::memset(output->mBuffers[i].mData, 0, output->mBuffers[i].mDataByteSize);
            if (!self.playing.load(std::memory_order_acquire)) return noErr;
            if (self.endpoint.buffer_index >= output->mNumberBuffers || !now || !output_time ||
                !(output_time->mFlags & kAudioTimeStampHostTimeValid)) {
                self.failure.store(Failure::callback);
                return noErr;
            }
            auto& target = output->mBuffers[self.endpoint.buffer_index];
            if (!target.mData || target.mNumberChannels != self.endpoint.actual.mChannelsPerFrame ||
                target.mDataByteSize % self.frame_bytes) {
                self.failure.store(Failure::callback);
                return noErr;
            }
            auto* destination = static_cast<std::uint8_t*>(target.mData);
            const auto first = self.ring.pop(destination, target.mDataByteSize);
            const auto block = coreaudio::complete_pcm_block(self.ring, destination, target.mDataByteSize,
                                                            first, self.eof.load(std::memory_order_acquire));
            const auto copied = block.bytes;
            const auto frames = copied / self.frame_bytes;
            if (frames) {
                self.submitted.fetch_add(frames, std::memory_order_relaxed);
                const auto delay = static_cast<std::uint64_t>((frames + self.endpoint.latency_frames) *
                                                            self.ticks_per_second / self.endpoint.output.sample_rate);
                self.last_frames.store(frames, std::memory_order_relaxed);
                self.end_host.store(output_time->mHostTime + delay, std::memory_order_release);
            }
            if (copied == target.mDataByteSize) self.empty_frames = 0;
            if (block.underrun) {
                self.underrun.store(true, std::memory_order_release);
                self.empty_frames += (target.mDataByteSize - copied) / self.frame_bytes;
                if (self.empty_frames >= static_cast<std::uint64_t>(self.endpoint.output.sample_rate) * 2)
                    self.failure.store(Failure::underrun);
            }
            return noErr;
        }
        std::int64_t position() const noexcept {
            auto frames = submitted.load(std::memory_order_acquire);
            const auto end = end_host.load(std::memory_order_acquire);
            const auto now = mach_absolute_time();
            if (end > now) {
                const auto pending = static_cast<std::uint64_t>((end - now) * endpoint.output.sample_rate / ticks_per_second);
                frames -= std::min({frames, last_frames.load(), pending});
            }
            const auto value = base_position.load() + static_cast<std::int64_t>(frames * 1000 / endpoint.output.sample_rate);
            return duration > 0 ? std::min(value, duration) : value;
        }
        void prebuffer() {
            const auto target = static_cast<std::size_t>(endpoint.output.sample_rate / 10) * frame_bytes;
            const auto deadline = std::chrono::steady_clock::now() + 3s;
            while (ring.readable() < target && !eof.load()) {
                require_active();
                if (std::chrono::steady_clock::now() >= deadline)
                    throw EngineError("media_error", "Media source did not provide the initial playback buffer");
                std::this_thread::sleep_for(2ms);
            }
        }
        void start_verified() {
            io_verified.store(false, std::memory_order_release);
            const auto cycle = callback_version.load(std::memory_order_acquire);
            endpoint.start_io();
            // Keep output silent through the first IO cycle and verify the
            // running stream properties before consuming any source PCM.
            const auto deadline = std::chrono::steady_clock::now() + 3s;
            while (callback_version.load(std::memory_order_acquire) < cycle + 2) {
                require_active();
                if (std::chrono::steady_clock::now() >= deadline)
                    throw EngineError("audio_error", "CoreAudio did not start its output callback");
                std::this_thread::sleep_for(2ms);
            }
            const auto integrity = endpoint.inspect(spec.exclusive);
            if (integrity) throw EngineError("device_lost", integrity.message(endpoint.actual));
            io_verified.store(true, std::memory_order_release);
            playing.store(true, std::memory_order_release);
        }
        void play() {
            std::lock_guard lock(resources);
            require_active();
            if (playing.load()) return;
            const auto integrity = endpoint.inspect(spec.exclusive);
            if (integrity) throw EngineError("device_lost", integrity.message(endpoint.actual));
            prebuffer();
            start_verified();
        }
        void pause() {
            std::lock_guard lock(resources);
            require_active();
            playing.store(false, std::memory_order_release);
            // Let already submitted frames reach the DAC before stopping HAL.
            // No new ring data is consumed while these frames drain.
            const auto limit = std::chrono::steady_clock::now() + 3s;
            while (((callback_version.load(std::memory_order_acquire) & 1) || mach_absolute_time() < end_host.load()) &&
                   std::chrono::steady_clock::now() < limit)
                std::this_thread::sleep_for(2ms);
            if (mach_absolute_time() < end_host.load())
                throw EngineError("audio_error", "CoreAudio output did not drain while pausing");
            endpoint.stop_io();
        }
        void require_active() const {
            if (stopping.load() || terminal.load() || failure.load() != Failure::none)
                throw EngineError("audio_error", "CoreAudio session is no longer active");
        }
        std::int64_t seek(std::int64_t requested) {
            if (!spec.seekable) throw EngineError("action_failed", "Media source is not seekable");
            if (requested < 0) throw EngineError("invalid_argument", "Seek position cannot be negative");
            seeking.store(true);
            std::unique_lock lock(resources);
            try {
                require_active();
                const bool resume = playing.exchange(false);
                endpoint.stop_io();
                decode_stop.store(true);
                spec.source.cancel();
                if (decode_thread.joinable()) decode_thread.join();
                spec.source.rearm();
                auto next = std::make_unique<Decoder>(spec.source.read, spec.size, spec.seekable);
                const auto& f = next->format();
                if (f.sample_rate != source.sample_rate || f.channels != source.channels ||
                    f.container_bits != source.container_bits || f.valid_bits != source.valid_bits ||
                    f.floating_point != source.floating_point || f.channel_mask != source.channel_mask || f.lossless != source.lossless)
                    throw EngineError("media_error", "Decoded PCM format changed while seeking");
                next->seek(duration > 0 ? std::min(requested, duration) : requested);
                const auto actual = next->position_ms();
                decoder = std::move(next);
                if (converter) converter = std::make_unique<SharedConverter>(source, endpoint.output, aligned_high());
                ring.clear();
                eof.store(false);
                submitted.store(0);
                last_frames.store(0);
                end_host.store(0);
                base_position.store(actual);
                empty_frames = 0;
                // A prior underrun remains visible for the entire media session.
                decode_stop.store(false);
                decode_thread = std::thread([this] { decode_loop(); });
                if (resume) { prebuffer(); start_verified(); }
                seeking.store(false);
                return actual;
            } catch (const EngineError&) { seeking.store(false); throw; }
              catch (...) { seeking.store(false); throw EngineError("media_error", "Media source failed while seeking"); }
        }
        void cancel_decode() noexcept {
            decode_stop.store(true);
            try { spec.source.cancel(); } catch (...) {}
        }
        bool stop_and_join() noexcept {
            stopping.store(true);
            playing.store(false);
            io_verified.store(false, std::memory_order_release);
            cancel_decode();
            if (decode_thread.joinable()) decode_thread.join();
            if (monitor_thread.joinable()) monitor_thread.join();
            std::lock_guard lock(resources);
            return endpoint.release();
        }
        std::string reason() const {
            if (!spec.exclusive) return "Shared CoreAudio mode uses system mixing and may convert samples";
            if (!io_verified.load(std::memory_order_acquire)) return "Exclusive PCM is negotiated; running output has not yet been verified";
            if (source.floating_point) return "Floating-point PCM is not reported as bit-transparent";
            if (!endpoint.mixing_disabled) return "Exact Hog-mode PCM; device does not expose verifiable mixing control";
            if (!endpoint.starting_channel_known) return "Device output channel volume mapping could not be verified";
            if (!hardware_unity.load()) return "Device volume or mute changes the output";
            if (underrun.load()) return "Output underrun inserted silence; this session is not bit-transparent";
            if (!spec.transformed_known) return "Source provenance is unknown";
            if (spec.transformed) return "Server reports transformed media";
            if (!source.lossless) return "Decoder reports a lossy source";
            return {};
        }
        void monitor_loop() noexcept {
            auto next_observation = std::chrono::steady_clock::now();
            while (!stopping.load()) {
                std::this_thread::sleep_for(10ms);
                if (seeking.load()) continue;
                bool ended = false;
                auto outcome = failure.load();
                IntegrityIssue integrity;
                {
                    std::lock_guard lock(resources);
                    if (seeking.load() || stopping.load()) continue;
                    integrity = endpoint.inspect(spec.exclusive);
                    if (integrity) outcome = Failure::device;
                    hardware_unity.store(endpoint.unity_controls());
                    const auto cycle = callback_version.load(std::memory_order_acquire);
                    ended = !(cycle & 1) && outcome == Failure::none && playing.load() && eof.load() &&
                            ring.readable() == 0 && mach_absolute_time() >= end_host.load() &&
                            callback_version.load(std::memory_order_acquire) == cycle;
                    if (outcome != Failure::none || ended) {
                        terminal.store(true);
                        playing.store(false);
                        cancel_decode();
                        if (!endpoint.release() && outcome == Failure::none) { outcome = Failure::restore; ended = false; }
                    }
                }
                if (outcome != Failure::none || ended) {
                    parent.terminal_event(this, ended, outcome, integrity);
                    break;
                }
                if (std::chrono::steady_clock::now() >= next_observation) {
                    parent.timeupdate(this);
                    next_observation = std::chrono::steady_clock::now() + 250ms;
                }
            }
        }
        Impl& parent;
        MediaSpec spec;
        std::unique_ptr<Decoder> decoder;
        const AudioFormat source;
        const std::int64_t duration;
        Endpoint endpoint;
        const std::size_t frame_bytes;
        coreaudio::PcmRing ring;
        std::unique_ptr<SharedConverter> converter;
        std::mutex resources;
        std::thread decode_thread, monitor_thread;
        std::atomic<bool> stopping{false}, decode_stop{false}, playing{false}, seeking{false}, terminal{false};
        std::atomic<bool> io_verified{false};
        std::atomic<bool> eof{false}, underrun{false}, hardware_unity{false};
        std::atomic<Failure> failure{Failure::none};
        std::atomic<double> gain{1};
        std::atomic<std::uint64_t> submitted{0}, end_host{0}, last_frames{0};
        std::atomic<std::uint64_t> callback_version{0};
        std::atomic<std::int64_t> base_position{0};
        std::uint64_t empty_frames = 0; // HAL-only, except when IO is stopped.
        double ticks_per_second = 0;
    };

    nlohmann::json snapshot() const {
        nlohmann::json result{{"state", state}, {"play_id", play_id}, {"sequence", sequence}, {"volume", volume}, {"actual", nullptr}};
        if (!session || state == "stopped" || state == "error") return result;
        const auto& s = *session;
        const auto reason = s.reason();
        result["actual"] = {{"device_id", s.endpoint.id}, {"name", s.endpoint.name},
            {"mode", s.spec.exclusive ? "exclusive" : "shared"}, {"sample_rate", s.endpoint.output.sample_rate},
            {"channels", s.endpoint.output.channels}, {"container_bits", s.endpoint.output.container_bits},
            {"valid_bits", s.endpoint.output.valid_bits}, {"bit_transparent", reason.empty()},
            {"reason", reason.empty() ? "Verified exact integer physical/virtual PCM, Hog ownership, mixing disabled, original lossless source" : reason}};
        return result;
    }
    std::shared_ptr<Session> transport(const std::string& expected, std::uint64_t requested) {
        std::lock_guard lock(mutex);
        if (!session || play_id != expected || state == "stopped" || state == "error")
            throw EngineError("action_failed", "No matching active media is loaded");
        if (requested < sequence) throw EngineError("action_failed", "Command sequence is stale");
        sequence = requested;
        return session;
    }
    void timeupdate(Session* expected) noexcept {
        try {
            nlohmann::json event;
            {
                std::lock_guard lock(mutex);
                if (session.get() != expected || state != "playing") return;
                event = {{"event", "observation"}, {"play_id", play_id}, {"sequence", sequence},
                    {"observation", observation("timeupdate", play_id, "playing", session->position(), session->duration)},
                    {"audio", snapshot()}};
            }
            emit(std::move(event));
        } catch (...) {}
    }
    void terminal_event(Session* expected, bool ended, Failure failure, const IntegrityIssue& integrity) noexcept {
        try {
            nlohmann::json event;
            {
                std::lock_guard lock(mutex);
                if (session.get() != expected) return;
                state = ended ? "stopped" : "error";
                event = {{"event", "observation"}, {"play_id", play_id}, {"sequence", sequence},
                    {"observation", observation(ended ? "ended" : "error", play_id, "", session->position(), session->duration)},
                    {"audio", snapshot()}};
                if (!ended) {
                    const char* code = failure == Failure::media ? "media_failed" : failure == Failure::device ? "device_lost" : "audio_error";
                    const char* message = failure == Failure::media ? "Media source could not be decoded" :
                        failure == Failure::device ? "CoreAudio device disconnected or its format/ownership changed; no fallback was selected" :
                        failure == Failure::underrun ? "CoreAudio output stopped after a sustained buffer underrun" :
                        failure == Failure::restore ? "CoreAudio could not restore the device settings" : "CoreAudio output buffer contract changed";
                    event["error"] = {{"code", code}, {"message", integrity ? integrity.message(session->endpoint.actual) : message}};
                }
            }
            emit(std::move(event));
        } catch (...) {}
    }
    void failed(const std::shared_ptr<Session>& current) {
        { std::lock_guard lock(mutex); if (session == current) state = "error"; }
        current->stop_and_join();
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
    std::string state = "stopped", play_id;
    std::uint64_t sequence = 0;
    double volume = 1;
};

std::vector<EndpointInfo> AudioEngine::enumerate_endpoints() {
    std::vector<EndpointInfo> result;
    AudioDeviceID default_device = 0;
    get(kAudioObjectSystemObject, address(kAudioHardwarePropertyDefaultOutputDevice), default_device);
    for (auto device : list<AudioDeviceID>(kAudioObjectSystemObject, address(kAudioHardwarePropertyDevices))) {
        UInt32 alive = 0;
        if (!get(device, address(kAudioDevicePropertyDeviceIsAlive), alive) || !alive) continue;
        try {
            if (list<AudioStreamID>(device, address(kAudioDevicePropertyStreams, kAudioObjectPropertyScopeOutput)).empty()) continue;
            auto id = text(device, kAudioDevicePropertyDeviceUID);
            if (!id.empty()) result.push_back({std::move(id), text(device, kAudioObjectPropertyName), device == default_device});
        } catch (const EngineError&) { /* A hot-unplug can race enumeration. */ }
    }
    return result;
}
AudioEngine::AudioEngine(Emit emit) : impl_(std::make_unique<Impl>(std::move(emit))) {}
AudioEngine::~AudioEngine() = default;
nlohmann::json AudioEngine::set_uri(MediaSpec spec) {
    if (spec.play_id.empty() || !spec.sequence || !spec.source.read || !spec.source.cancel || !spec.source.rearm)
        throw EngineError("invalid_argument", "set_uri requires media identity and source callbacks");
    if (!std::isfinite(spec.volume) || spec.volume < 0 || spec.volume > 1)
        throw EngineError("invalid_argument", "Volume must be between zero and one");
    if (spec.exclusive && spec.volume != 1)
        throw EngineError("exclusive_volume", "Exclusive CoreAudio requires unity volume");
    std::shared_ptr<Impl::Session> old;
    {
        std::lock_guard lock(impl_->mutex);
        if (impl_->state != "stopped" && impl_->state != "error")
            throw EngineError("stop_required", "Stop native audio before loading another media item");
        old = std::move(impl_->session);
    }
    if (old && !old->stop_and_join()) throw EngineError("audio_error", "CoreAudio could not restore the previous device settings");
    std::shared_ptr<Impl::Session> candidate;
    try { candidate = std::make_shared<Impl::Session>(*impl_, std::move(spec)); }
    catch (const SourceError&) { throw EngineError("media_failed", "Media source could not be read"); }
    catch (const DecodeError&) { throw EngineError("media_failed", "Media could not be decoded"); }
    {
        std::lock_guard lock(impl_->mutex);
        impl_->session = candidate;
        impl_->play_id = candidate->spec.play_id;
        impl_->sequence = candidate->spec.sequence;
        impl_->volume = candidate->spec.volume;
        impl_->state = "loaded";
    }
    try { candidate->start_workers(); } catch (...) { impl_->failed(candidate); throw; }
    std::lock_guard lock(impl_->mutex);
    return {{"observation", observation("loaded", impl_->play_id, "paused", 0, candidate->duration)}, {"audio", impl_->snapshot()}};
}
nlohmann::json AudioEngine::play(const std::string& play_id, std::uint64_t sequence) {
    const auto current = impl_->transport(play_id, sequence);
    try { current->play(); } catch (...) { impl_->failed(current); throw; }
    std::lock_guard lock(impl_->mutex);
    if (current->terminal.load()) throw EngineError("audio_error", "CoreAudio session ended while starting");
    impl_->state = "playing";
    return {{"observation", observation("playing", play_id, "playing", current->position(), current->duration)}, {"audio", impl_->snapshot()}};
}
nlohmann::json AudioEngine::pause(const std::string& play_id, std::uint64_t sequence) {
    const auto current = impl_->transport(play_id, sequence);
    try { current->pause(); } catch (...) { impl_->failed(current); throw; }
    std::lock_guard lock(impl_->mutex);
    if (current->terminal.load()) throw EngineError("audio_error", "CoreAudio session ended while pausing");
    impl_->state = "paused";
    return {{"observation", observation("pause", play_id, "paused", current->position(), current->duration)}, {"audio", impl_->snapshot()}};
}
nlohmann::json AudioEngine::seek(const std::string& play_id, std::uint64_t sequence, std::int64_t position_ms) {
    const auto current = impl_->transport(play_id, sequence);
    std::int64_t actual;
    try { actual = current->seek(position_ms); }
    catch (const EngineError& error) {
        if (error.code() != "action_failed" && error.code() != "invalid_argument") impl_->failed(current);
        throw;
    }
    std::lock_guard lock(impl_->mutex);
    if (current->terminal.load()) throw EngineError("audio_error", "CoreAudio session ended while seeking");
    const auto state = impl_->state == "playing" ? "playing" : "paused";
    impl_->state = state;
    return {{"observation", observation("seeked", play_id, state, actual, current->duration)}, {"audio", impl_->snapshot()}};
}
nlohmann::json AudioEngine::stop(const std::string& play_id, std::uint64_t sequence) {
    std::shared_ptr<Impl::Session> old;
    {
        std::lock_guard lock(impl_->mutex);
        if (!impl_->play_id.empty() && impl_->play_id != play_id) throw EngineError("action_failed", "Media identity changed");
        if (sequence < impl_->sequence) throw EngineError("action_failed", "Command sequence is stale");
        impl_->sequence = sequence;
        old = std::move(impl_->session);
        impl_->state = "stopped";
        impl_->play_id.clear();
    }
    const bool restored = !old || old->stop_and_join();
    std::lock_guard lock(impl_->mutex);
    if (!restored) { impl_->state = "error"; throw EngineError("audio_error", "CoreAudio could not restore device settings"); }
    return {{"observation", observation("stopped", play_id, "paused", old ? old->position() : 0, old ? old->duration : 0, old != nullptr)},
            {"audio", impl_->snapshot()}};
}
nlohmann::json AudioEngine::set_volume(double volume) {
    if (!std::isfinite(volume) || volume < 0 || volume > 1) throw EngineError("invalid_argument", "Volume must be between zero and one");
    std::lock_guard lock(impl_->mutex);
    if (impl_->session && impl_->session->spec.exclusive && volume != 1)
        throw EngineError("exclusive_volume", "Exclusive CoreAudio requires unity volume");
    impl_->volume = volume;
    if (impl_->session) impl_->session->gain.store(volume);
    return impl_->snapshot();
}
nlohmann::json AudioEngine::status() const { std::lock_guard lock(impl_->mutex); return impl_->snapshot(); }
void AudioEngine::shutdown() { impl_->shutdown(); }
} // namespace jastreamer
