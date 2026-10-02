#pragma once

#include "decoder.hpp"
#include <CoreAudio/CoreAudio.h>
#include <cmath>
#include <optional>
#include <span>

namespace jastreamer::coreaudio {
inline bool same(const AudioStreamBasicDescription& a, const AudioStreamBasicDescription& b) noexcept {
    return a.mSampleRate == b.mSampleRate && a.mFormatID == b.mFormatID &&
        a.mFormatFlags == b.mFormatFlags && a.mBytesPerPacket == b.mBytesPerPacket &&
        a.mFramesPerPacket == b.mFramesPerPacket && a.mBytesPerFrame == b.mBytesPerFrame &&
        a.mChannelsPerFrame == b.mChannelsPerFrame && a.mBitsPerChannel == b.mBitsPerChannel;
}
inline bool usable(const AudioStreamBasicDescription& f) noexcept {
    if (f.mFormatID != kAudioFormatLinearPCM || f.mFramesPerPacket != 1 ||
        (f.mFormatFlags & (kAudioFormatFlagIsBigEndian | kAudioFormatFlagIsNonInterleaved)) ||
        !std::isfinite(f.mSampleRate) || f.mSampleRate < 8000 || f.mSampleRate > 768000 ||
        f.mSampleRate != std::floor(f.mSampleRate) || f.mChannelsPerFrame == 0 || f.mChannelsPerFrame > 32 ||
        f.mBytesPerFrame % f.mChannelsPerFrame || f.mBytesPerPacket != f.mBytesPerFrame) return false;
    const auto bits = f.mBytesPerFrame / f.mChannelsPerFrame * 8;
    if (f.mBitsPerChannel == 0 || f.mBitsPerChannel > bits) return false;
    if (f.mFormatFlags & kAudioFormatFlagIsFloat)
        return (bits == 32 || bits == 64) && f.mBitsPerChannel == bits;
    return (f.mFormatFlags & kAudioFormatFlagIsSignedInteger) &&
        (bits == 8 || bits == 16 || bits == 24 || bits == 32 || bits == 64);
}

// HAL's physical and virtual lists differ: USB devices commonly advertise
// mixable physical integers but only nonmixable virtual integers. Choose their
// exact intersection rather than asking HAL to convert a mixable integer
// request to its default float representation. Precision may increase only by
// appending zero LSBs in our integer packer, never by reducing source bits.
inline std::optional<AudioStreamBasicDescription> exclusive_format(
    const AudioFormat& source, std::span<const AudioStreamRangedDescription> physical,
    std::span<const AudioStreamRangedDescription> virtual_formats) noexcept {
    std::optional<AudioStreamBasicDescription> selected;
    for (const auto& range : physical) {
        auto candidate = range.mFormat;
        candidate.mSampleRate = source.sample_rate;
        if (source.sample_rate < range.mSampleRateRange.mMinimum || source.sample_rate > range.mSampleRateRange.mMaximum ||
            !usable(candidate) || ((candidate.mFormatFlags & kAudioFormatFlagIsFloat) != 0) != source.floating_point ||
            (source.floating_point ? candidate.mBitsPerChannel != static_cast<UInt32>(source.valid_bits)
                                   : candidate.mBitsPerChannel < static_cast<UInt32>(source.valid_bits)) ||
            candidate.mChannelsPerFrame != static_cast<UInt32>(source.channels)) continue;
        bool virtual_match = false;
        for (const auto& virtual_range : virtual_formats) {
            auto virtual_candidate = virtual_range.mFormat;
            virtual_candidate.mSampleRate = source.sample_rate;
            if (source.sample_rate >= virtual_range.mSampleRateRange.mMinimum &&
                source.sample_rate <= virtual_range.mSampleRateRange.mMaximum && same(candidate, virtual_candidate)) {
                virtual_match = true;
                break;
            }
        }
        if (!virtual_match) continue;
        const bool nonmixable = (candidate.mFormatFlags & kAudioFormatFlagIsNonMixable) != 0;
        const bool selected_nonmixable = selected && (selected->mFormatFlags & kAudioFormatFlagIsNonMixable);
        if (!selected || (nonmixable && !selected_nonmixable) ||
            (nonmixable == selected_nonmixable &&
             (candidate.mBitsPerChannel < selected->mBitsPerChannel ||
              (candidate.mBitsPerChannel == selected->mBitsPerChannel && candidate.mBytesPerFrame < selected->mBytesPerFrame))))
            selected = candidate;
    }
    return selected;
}
} // namespace jastreamer::coreaudio
