#include "decoder.hpp"

#include <algorithm>
#include <bit>
#include <cerrno>
#include <cstdio>
#include <climits>
#include <cstring>
#include <limits>
#include <optional>
#include <string>
#include <utility>

extern "C" {
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavutil/channel_layout.h>
#include <libavutil/error.h>
#include <libavutil/mem.h>
#include <libavutil/samplefmt.h>
}

namespace jastreamer {
namespace {

constexpr int kAvioBufferSize = 32 * 1024;

std::string ffmpeg_error(const char* operation, int error) {
    char message[AV_ERROR_MAX_STRING_SIZE]{};
    av_strerror(error, message, sizeof(message));
    return std::string(operation) + ": " + message;
}

std::int64_t rescale_nearest(std::int64_t value, AVRational source, AVRational destination) {
    return av_rescale_q_rnd(
        value,
        source,
        destination,
        static_cast<AVRounding>(AV_ROUND_NEAR_INF | AV_ROUND_PASS_MINMAX));
}

} // namespace

class Decoder::Impl {
public:
    Impl(ReadBytes read_bytes, std::int64_t size, bool seekable)
        : read_bytes_(std::move(read_bytes)), size_(size), seekable_(seekable) {
        static_assert(std::endian::native == std::endian::little,
                      "Decoder PCM output requires a little-endian target");

        if (!read_bytes_) {
            throw DecodeError("media source reader is missing");
        }
        if (size_ < -1) {
            throw DecodeError("media source size is invalid");
        }
        try {

        auto* buffer = static_cast<unsigned char*>(av_malloc(kAvioBufferSize));
        if (buffer == nullptr) {
            throw DecodeError("could not allocate media input buffer");
        }
        avio_context_ = avio_alloc_context(
            buffer,
            kAvioBufferSize,
            0,
            this,
            &Impl::read_packet,
            nullptr,
            &Impl::seek_packet);
        if (avio_context_ == nullptr) {
            av_free(buffer);
            throw DecodeError("could not create media input");
        }
        avio_context_->seekable = seekable_ ? AVIO_SEEKABLE_NORMAL : 0;

        format_context_ = avformat_alloc_context();
        if (format_context_ == nullptr) {
            throw DecodeError("could not allocate media demuxer");
        }
        format_context_->pb = avio_context_;
        format_context_->flags |= AVFMT_FLAG_CUSTOM_IO;

        int result = avformat_open_input(&format_context_, nullptr, nullptr, nullptr);
        throw_source_failure();
        if (result < 0) {
            throw DecodeError(ffmpeg_error("could not open media", result));
        }

        result = avformat_find_stream_info(format_context_, nullptr);
        throw_source_failure();
        if (result < 0) {
            throw DecodeError(ffmpeg_error("could not inspect media", result));
        }

        const AVCodec* codec = nullptr;
        audio_stream_index_ = av_find_best_stream(
            format_context_, AVMEDIA_TYPE_AUDIO, -1, -1, &codec, 0);
        if (audio_stream_index_ < 0 || codec == nullptr) {
            throw DecodeError(audio_stream_index_ < 0
                                  ? ffmpeg_error("media has no decodable audio stream", audio_stream_index_)
                                  : "media has no decoder");
        }
        audio_stream_ = format_context_->streams[audio_stream_index_];
        if (audio_stream_ == nullptr || audio_stream_->codecpar == nullptr) {
            throw DecodeError("media audio stream is invalid");
        }

        codec_context_ = avcodec_alloc_context3(codec);
        if (codec_context_ == nullptr) {
            throw DecodeError("could not allocate audio decoder");
        }
        result = avcodec_parameters_to_context(codec_context_, audio_stream_->codecpar);
        if (result < 0) {
            throw DecodeError(ffmpeg_error("could not configure audio decoder", result));
        }
        codec_context_->pkt_timebase = audio_stream_->time_base;
        codec_context_->err_recognition = AV_EF_CRCCHECK | AV_EF_CAREFUL | AV_EF_EXPLODE;
        result = avcodec_open2(codec_context_, codec, nullptr);
        if (result < 0) {
            throw DecodeError(ffmpeg_error("could not start audio decoder", result));
        }

        const AVCodecDescriptor* descriptor = avcodec_descriptor_get(codec_context_->codec_id);
        lossless_ = descriptor != nullptr &&
                    (descriptor->props & AV_CODEC_PROP_LOSSLESS) != 0 &&
                    (descriptor->props & AV_CODEC_PROP_LOSSY) == 0 &&
                    codec_context_->codec_id != AV_CODEC_ID_DSD_LSBF &&
                    codec_context_->codec_id != AV_CODEC_ID_DSD_MSBF &&
                    codec_context_->codec_id != AV_CODEC_ID_DSD_LSBF_PLANAR &&
                    codec_context_->codec_id != AV_CODEC_ID_DSD_MSBF_PLANAR;
        set_container_duration();

        packet_ = av_packet_alloc();
        frame_ = av_frame_alloc();
        if (packet_ == nullptr || frame_ == nullptr) {
            throw DecodeError("could not allocate audio decode buffers");
        }
        if (!decode_next_frame()) {
            throw DecodeError("media contains no decodable audio samples");
        }
        } catch (...) {
            cleanup();
            throw;
        }
    }

    ~Impl() {
        cleanup();
    }

    const AudioFormat& format() const noexcept {
        return output_format_;
    }

    std::int64_t duration_ms() const noexcept {
        return duration_ms_;
    }

    std::size_t read(std::uint8_t* destination, std::size_t requested_frames) {
        if (requested_frames == 0) {
            return 0;
        }
        if (destination == nullptr) {
            throw DecodeError("audio output destination is missing");
        }
        const std::size_t bytes_per_frame =
            static_cast<std::size_t>(output_format_.channels) *
            static_cast<std::size_t>(bytes_per_sample_);
        if (requested_frames > std::numeric_limits<std::size_t>::max() / bytes_per_frame) {
            throw DecodeError("requested audio output is too large");
        }

        std::size_t written = 0;
        while (written < requested_frames) {
            if (frame_offset_ >= frame_->nb_samples && !decode_next_frame()) {
                if (decoded_from_start_) {
                    duration_ms_ = samples_to_ms(position_samples_);
                }
                break;
            }

            const std::size_t available =
                static_cast<std::size_t>(frame_->nb_samples - frame_offset_);
            const std::size_t count = std::min(available, requested_frames - written);
            copy_samples(destination, written, frame_offset_, count);
            frame_offset_ += static_cast<int>(count);
            written += count;

            if (count > static_cast<std::size_t>(
                            std::numeric_limits<std::int64_t>::max() - position_samples_)) {
                throw DecodeError("decoded audio position overflowed");
            }
            position_samples_ += static_cast<std::int64_t>(count);
        }
        return written;
    }

    void seek(std::int64_t position_ms) {
        if (!seekable_) {
            throw DecodeError("media source is not seekable");
        }
        if (position_ms < 0) {
            throw DecodeError("seek position is negative");
        }
        if (duration_ms_ >= 0) {
            position_ms = std::min(position_ms, duration_ms_);
        }

        const std::int64_t target_samples = rescale_nearest(
            position_ms, AVRational{1, 1000}, AVRational{1, output_format_.sample_rate});
        const std::int64_t start_timestamp = stream_start_timestamp();
        const std::int64_t relative_timestamp = rescale_nearest(
            target_samples,
            AVRational{1, output_format_.sample_rate},
            audio_stream_->time_base);
        if ((relative_timestamp > 0 &&
             start_timestamp > std::numeric_limits<std::int64_t>::max() - relative_timestamp) ||
            (relative_timestamp < 0 &&
             start_timestamp < std::numeric_limits<std::int64_t>::min() - relative_timestamp)) {
            throw DecodeError("seek position is out of range");
        }
        const std::int64_t timestamp = start_timestamp + relative_timestamp;

        const int result = avformat_seek_file(
            format_context_,
            audio_stream_index_,
            std::numeric_limits<std::int64_t>::min(),
            timestamp,
            timestamp,
            AVSEEK_FLAG_BACKWARD);
        throw_source_failure();
        if (result < 0) {
            throw DecodeError(ffmpeg_error("could not seek media", result));
        }

        avcodec_flush_buffers(codec_context_);
        av_packet_unref(packet_);
        av_frame_unref(frame_);
        frame_offset_ = 0;
        demux_eof_ = false;
        drain_sent_ = false;
        decoder_eof_ = false;
        seek_target_samples_ = target_samples;
        position_samples_ = target_samples;
        decoded_from_start_ = target_samples == 0;
    }

    std::int64_t position_ms() const noexcept {
        return samples_to_ms(position_samples_);
    }

private:
    void cleanup() noexcept {
        if (frame_ != nullptr) {
            av_frame_free(&frame_);
        }
        if (packet_ != nullptr) {
            av_packet_free(&packet_);
        }
        if (codec_context_ != nullptr) {
            avcodec_free_context(&codec_context_);
        }
        if (format_context_ != nullptr) {
            avformat_close_input(&format_context_);
        }
        if (avio_context_ != nullptr) {
            av_freep(&avio_context_->buffer);
            avio_context_free(&avio_context_);
        }
    }

    static int read_packet(void* opaque, std::uint8_t* destination, int count) noexcept {
        auto& self = *static_cast<Impl*>(opaque);
        if (count <= 0) {
            return AVERROR(EINVAL);
        }
        if (self.source_failed_) {
            return AVERROR_EXTERNAL;
        }
        if (self.size_ >= 0 && self.io_offset_ >= self.size_) {
            return AVERROR_EOF;
        }

        std::size_t bounded = std::min<std::size_t>(
            static_cast<std::size_t>(count), kAvioBufferSize);
        if (self.size_ >= 0) {
            bounded = static_cast<std::size_t>(
                std::min<std::int64_t>(static_cast<std::int64_t>(bounded),
                                       self.size_ - self.io_offset_));
        }
        try {
            const std::int64_t result =
                self.read_bytes_(self.io_offset_, destination, bounded);
            if (result < 0) {
                self.set_source_failure("media source read failed");
                return AVERROR_EXTERNAL;
            }
            if (result == 0) {
                return AVERROR_EOF;
            }
            if (result > static_cast<std::int64_t>(bounded)) {
                self.set_source_failure("media source returned too many bytes");
                return AVERROR_EXTERNAL;
            }
            self.io_offset_ += result;
            return static_cast<int>(result);
        } catch (const SourceError& error) {
            self.set_source_failure(error.what());
        } catch (const std::exception& error) {
            self.set_source_failure(error.what());
        } catch (...) {
            self.set_source_failure("media source read threw an unknown error");
        }
        return AVERROR_EXTERNAL;
    }

    static std::int64_t seek_packet(void* opaque, std::int64_t offset, int whence) noexcept {
        auto& self = *static_cast<Impl*>(opaque);
        if ((whence & AVSEEK_SIZE) != 0) {
            return self.size_ >= 0 ? self.size_ : AVERROR(ENOSYS);
        }
        if (!self.seekable_) {
            return AVERROR(ENOSYS);
        }

        whence &= ~AVSEEK_FORCE;
        std::int64_t base = 0;
        switch (whence) {
        case SEEK_SET:
            break;
        case SEEK_CUR:
            base = self.io_offset_;
            break;
        case SEEK_END:
            if (self.size_ < 0) {
                return AVERROR(ENOSYS);
            }
            base = self.size_;
            break;
        default:
            return AVERROR(EINVAL);
        }

        if ((offset > 0 && base > std::numeric_limits<std::int64_t>::max() - offset) ||
            (offset < 0 && base < std::numeric_limits<std::int64_t>::min() - offset)) {
            return AVERROR(EINVAL);
        }
        const std::int64_t position = base + offset;
        if (position < 0 || (self.size_ >= 0 && position > self.size_)) {
            return AVERROR(EINVAL);
        }
        self.io_offset_ = position;
        return position;
    }

    void set_source_failure(const char* message) noexcept {
        if (source_failed_) {
            return;
        }
        source_failed_ = true;
        try {
            source_error_ = message != nullptr && message[0] != '\0'
                                ? message
                                : "media source read failed";
        } catch (...) {
            source_error_.clear();
        }
    }

    void throw_source_failure() const {
        if (source_failed_) {
            throw SourceError(source_error_.empty() ? "media source read failed" : source_error_);
        }
    }

    void set_container_duration() {
        if (audio_stream_->duration != AV_NOPTS_VALUE && audio_stream_->duration >= 0) {
            duration_ms_ = rescale_nearest(
                audio_stream_->duration, audio_stream_->time_base, AVRational{1, 1000});
            return;
        }
        if (format_context_->duration != AV_NOPTS_VALUE && format_context_->duration >= 0) {
            duration_ms_ = rescale_nearest(
                format_context_->duration, AV_TIME_BASE_Q, AVRational{1, 1000});
        }
    }

    bool decode_next_frame() {
        av_frame_unref(frame_);
        frame_offset_ = 0;

        while (!decoder_eof_) {
            int result = avcodec_receive_frame(codec_context_, frame_);
            if (result == 0) {
                if (frame_->nb_samples <= 0) {
                    av_frame_unref(frame_);
                    continue;
                }
                validate_frame();
                if (apply_seek_target()) {
                    return true;
                }
                av_frame_unref(frame_);
                continue;
            }
            if (result == AVERROR_EOF) {
                decoder_eof_ = true;
                break;
            }
            if (result != AVERROR(EAGAIN)) {
                throw_source_failure();
                throw DecodeError(ffmpeg_error("could not decode audio", result));
            }

            if (demux_eof_) {
                if (!drain_sent_) {
                    result = avcodec_send_packet(codec_context_, nullptr);
                    if (result < 0 && result != AVERROR_EOF) {
                        throw DecodeError(ffmpeg_error("could not drain audio decoder", result));
                    }
                    drain_sent_ = true;
                    continue;
                }
                throw DecodeError("audio decoder stopped before reaching end of stream");
            }

            for (;;) {
                result = av_read_frame(format_context_, packet_);
                throw_source_failure();
                if (result == AVERROR_EOF) {
                    demux_eof_ = true;
                    break;
                }
                if (result < 0) {
                    throw DecodeError(ffmpeg_error("could not read media packet", result));
                }
                if (packet_->stream_index != audio_stream_index_) {
                    av_packet_unref(packet_);
                    continue;
                }

                result = avcodec_send_packet(codec_context_, packet_);
                av_packet_unref(packet_);
                if (result < 0) {
                    throw DecodeError(ffmpeg_error("could not submit audio packet", result));
                }
                break;
            }
        }
        return false;
    }

    void validate_frame() {
        const auto sample_format = static_cast<AVSampleFormat>(frame_->format);
        const int bytes_per_sample = av_get_bytes_per_sample(sample_format);
        const int channels = frame_->ch_layout.nb_channels;
        const int sample_rate = frame_->sample_rate;
        if (bytes_per_sample <= 0 || channels <= 0 || sample_rate <= 0) {
            throw DecodeError("decoder returned an invalid audio format");
        }

        bool floating_point = false;
        switch (av_get_packed_sample_fmt(sample_format)) {
        case AV_SAMPLE_FMT_U8:
        case AV_SAMPLE_FMT_S16:
        case AV_SAMPLE_FMT_S32:
        case AV_SAMPLE_FMT_S64:
            break;
        case AV_SAMPLE_FMT_FLT:
        case AV_SAMPLE_FMT_DBL:
            floating_point = true;
            break;
        default:
            throw DecodeError("decoder returned an unsupported audio sample format");
        }

        std::uint32_t channel_mask = 0;
        if (frame_->ch_layout.order == AV_CHANNEL_ORDER_NATIVE) {
            if ((frame_->ch_layout.u.mask >> 32U) != 0) {
                throw DecodeError("audio channel layout cannot be represented by the output contract");
            }
            channel_mask = static_cast<std::uint32_t>(frame_->ch_layout.u.mask);
        } else if (frame_->ch_layout.order != AV_CHANNEL_ORDER_UNSPEC) {
            throw DecodeError("audio channel layout is not a native speaker layout");
        }

        if (!format_initialized_) {
            int valid_bits = bytes_per_sample * CHAR_BIT;
            if (!floating_point) {
                if (codec_context_->bits_per_raw_sample > 0) {
                    valid_bits = codec_context_->bits_per_raw_sample;
                } else if (audio_stream_->codecpar->bits_per_raw_sample > 0) {
                    valid_bits = audio_stream_->codecpar->bits_per_raw_sample;
                } else if (lossless_) {
                    const int codec_bits = av_get_bits_per_sample(codec_context_->codec_id);
                    if (codec_bits > 0) {
                        valid_bits = codec_bits;
                    }
                }
                valid_bits = std::clamp(valid_bits, 1, bytes_per_sample * CHAR_BIT);
            }

            sample_format_ = sample_format;
            bytes_per_sample_ = bytes_per_sample;
            planar_ = av_sample_fmt_is_planar(sample_format) != 0;
            output_format_ = AudioFormat{
                sample_rate,
                channels,
                bytes_per_sample * CHAR_BIT,
                valid_bits,
                floating_point,
                channel_mask,
                lossless_,
            };
            format_initialized_ = true;
            return;
        }

        if (sample_format != sample_format_ || sample_rate != output_format_.sample_rate ||
            channels != output_format_.channels || channel_mask != output_format_.channel_mask) {
            throw DecodeError("audio format changes within the stream are unsupported");
        }
    }

    bool apply_seek_target() {
        if (!seek_target_samples_.has_value()) {
            return true;
        }

        const std::int64_t target = *seek_target_samples_;
        if (frame_->best_effort_timestamp == AV_NOPTS_VALUE) {
            seek_target_samples_.reset();
            return true;
        }

        const std::int64_t start_timestamp = stream_start_timestamp();
        if ((start_timestamp < 0 &&
             frame_->best_effort_timestamp >
                 std::numeric_limits<std::int64_t>::max() + start_timestamp) ||
            (start_timestamp > 0 &&
             frame_->best_effort_timestamp <
                 std::numeric_limits<std::int64_t>::min() + start_timestamp)) {
            throw DecodeError("decoded audio timestamp is out of range");
        }
        const std::int64_t relative_timestamp =
            frame_->best_effort_timestamp - start_timestamp;
        const std::int64_t frame_start = rescale_nearest(
            relative_timestamp,
            audio_stream_->time_base,
            AVRational{1, output_format_.sample_rate});
        if (frame_start > std::numeric_limits<std::int64_t>::max() - frame_->nb_samples) {
            throw DecodeError("decoded audio timestamp is out of range");
        }
        const std::int64_t frame_end = frame_start + frame_->nb_samples;
        if (frame_end <= target) {
            return false;
        }
        if (frame_start < target) {
            frame_offset_ = static_cast<int>(std::min<std::int64_t>(
                target - frame_start, frame_->nb_samples));
        } else if (frame_start > target) {
            position_samples_ = frame_start;
        }
        seek_target_samples_.reset();
        return frame_offset_ < frame_->nb_samples;
    }

    void copy_samples(std::uint8_t* destination,
                      std::size_t destination_frame,
                      int source_frame,
                      std::size_t frames) const {
        const std::size_t channels = static_cast<std::size_t>(output_format_.channels);
        const std::size_t sample_bytes = static_cast<std::size_t>(bytes_per_sample_);
        std::uint8_t* output = destination + destination_frame * channels * sample_bytes;

        if (!planar_) {
            const std::size_t source_offset =
                static_cast<std::size_t>(source_frame) * channels * sample_bytes;
            std::memcpy(output, frame_->extended_data[0] + source_offset,
                        frames * channels * sample_bytes);
            return;
        }

        for (std::size_t frame = 0; frame < frames; ++frame) {
            for (std::size_t channel = 0; channel < channels; ++channel) {
                const std::size_t source_offset =
                    (static_cast<std::size_t>(source_frame) + frame) * sample_bytes;
                std::memcpy(output, frame_->extended_data[channel] + source_offset, sample_bytes);
                output += sample_bytes;
            }
        }
    }

    std::int64_t stream_start_timestamp() const noexcept {
        return audio_stream_->start_time == AV_NOPTS_VALUE ? 0 : audio_stream_->start_time;
    }

    std::int64_t samples_to_ms(std::int64_t samples) const noexcept {
        return rescale_nearest(
            samples, AVRational{1, output_format_.sample_rate}, AVRational{1, 1000});
    }

    ReadBytes read_bytes_;
    std::int64_t size_ = -1;
    bool seekable_ = false;
    std::int64_t io_offset_ = 0;
    bool source_failed_ = false;
    std::string source_error_;

    AVIOContext* avio_context_ = nullptr;
    AVFormatContext* format_context_ = nullptr;
    AVCodecContext* codec_context_ = nullptr;
    AVPacket* packet_ = nullptr;
    AVFrame* frame_ = nullptr;
    AVStream* audio_stream_ = nullptr;
    int audio_stream_index_ = -1;

    bool demux_eof_ = false;
    bool drain_sent_ = false;
    bool decoder_eof_ = false;
    bool lossless_ = false;
    bool format_initialized_ = false;
    AVSampleFormat sample_format_ = AV_SAMPLE_FMT_NONE;
    int bytes_per_sample_ = 0;
    bool planar_ = false;
    int frame_offset_ = 0;
    AudioFormat output_format_{};
    std::int64_t duration_ms_ = -1;
    std::int64_t position_samples_ = 0;
    bool decoded_from_start_ = true;
    std::optional<std::int64_t> seek_target_samples_;
};

Decoder::Decoder(ReadBytes read_bytes, std::int64_t size, bool seekable)
    : impl_(std::make_unique<Impl>(std::move(read_bytes), size, seekable)) {}

Decoder::~Decoder() = default;

const AudioFormat& Decoder::format() const {
    return impl_->format();
}

std::int64_t Decoder::duration_ms() const {
    return impl_->duration_ms();
}

std::size_t Decoder::read(std::uint8_t* destination, std::size_t frames) {
    return impl_->read(destination, frames);
}

void Decoder::seek(std::int64_t position_ms) {
    impl_->seek(position_ms);
}

std::int64_t Decoder::position_ms() const {
    return impl_->position_ms();
}

} // namespace jastreamer
