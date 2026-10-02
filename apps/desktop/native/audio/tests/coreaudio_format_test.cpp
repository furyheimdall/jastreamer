#include "coreaudio_format.hpp"

#include <array>
#include <iostream>
#include <stdexcept>

namespace {
void require(bool value, const char* message) { if (!value) throw std::runtime_error(message); }
AudioStreamRangedDescription integer(unsigned bits, bool nonmixable, double rate = 96000) {
    AudioStreamBasicDescription format{};
    format.mSampleRate = rate;
    format.mFormatID = kAudioFormatLinearPCM;
    format.mFormatFlags = kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked |
                         (nonmixable ? kAudioFormatFlagIsNonMixable : 0);
    format.mBytesPerPacket = format.mBytesPerFrame = 2 * bits / 8;
    format.mFramesPerPacket = 1;
    format.mChannelsPerFrame = 2;
    format.mBitsPerChannel = bits;
    return {format, {rate, rate}};
}
}
int main() {
    try {
        using jastreamer::coreaudio::exclusive_format;
        // A real USB DAC capability shape: physical integers have both modes,
        // but virtual integers exist only with the HAL nonmixable flag.
        const std::array physical{integer(32, false), integer(16, false), integer(32, true), integer(16, true)};
        const std::array virtual_formats{integer(32, true), integer(16, true)};
        jastreamer::AudioFormat source{96000, 2, 16, 16, false, 3, true};
        auto selected = exclusive_format(source, physical, virtual_formats);
        require(selected && selected->mBitsPerChannel == 16 &&
                (selected->mFormatFlags & kAudioFormatFlagIsNonMixable),
                "A physical-only mixable integer must not be selected over the exact nonmixable intersection");
        source.container_bits = 32;
        source.valid_bits = 24;
        selected = exclusive_format(source, physical, virtual_formats);
        require(selected && selected->mBitsPerChannel == 32 && selected->mBytesPerFrame == 8 &&
                (selected->mFormatFlags & kAudioFormatFlagIsNonMixable),
                "24-bit source must use a zero-widenable exact 32-bit physical/virtual word");
        const std::array narrow{integer(16, true)};
        require(!exclusive_format(source, narrow, narrow), "Exclusive selection must never discard source precision");
        source.sample_rate = 192000;
        require(!exclusive_format(source, physical, virtual_formats), "Exclusive selection must never resample to an advertised different rate");
        source.sample_rate = 96000;
        source.channels = 1;
        require(!exclusive_format(source, physical, virtual_formats), "Exclusive selection must never silently change channel count");
        source.channels = 2;
        auto floating = integer(32, false);
        floating.mFormat.mFormatFlags = kAudioFormatFlagIsFloat | kAudioFormatFlagIsPacked;
        const std::array float_only{floating};
        require(!exclusive_format(source, physical, float_only), "Integer source cannot use a float-only virtual path");
        source.floating_point = true;
        source.valid_bits = 32;
        selected = exclusive_format(source, float_only, float_only);
        require(selected && (selected->mFormatFlags & kAudioFormatFlagIsFloat), "Float exact-mode negotiation must remain distinct from integer transparency");
        std::cout << "CoreAudio exact physical/virtual format selection passed\n";
        return 0;
    } catch (const std::exception& error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
