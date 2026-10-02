#include "media_controls_macos.hpp"

#import <AppKit/AppKit.h>
#import <ImageIO/ImageIO.h>
#import <MediaPlayer/MediaPlayer.h>

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <mutex>
#include <optional>
#include <stdexcept>
#include <string>
#include <string_view>
#include <thread>
#include <type_traits>
#include <utility>
#include <vector>

namespace jastreamer {
namespace {

NSString* text(const std::string& value) {
    NSString* result = [[NSString alloc] initWithBytes:value.data()
        length:value.size() encoding:NSUTF8StringEncoding];
    if (!result) throw std::invalid_argument("Invalid media metadata UTF-8");
    return result;
}

MPMediaItemArtwork* artwork(const std::string& encoded) {
    if (encoded.empty()) return nil;
    // Bound compressed input, source dimensions and the decoded image independently.
    if (encoded.size() > 192 * 1024)
        throw std::invalid_argument("Media artwork is too large");
    NSData* data = [[NSData alloc] initWithBase64EncodedString:text(encoded) options:0];
    if (!data || data.length == 0 || data.length > 144 * 1024)
        throw std::invalid_argument("Invalid media artwork encoding");
    const std::unique_ptr<std::remove_pointer_t<CGImageSourceRef>, decltype(&CFRelease)> source(
        CGImageSourceCreateWithData((__bridge CFDataRef)data,
            (__bridge CFDictionaryRef)@{(__bridge NSString*)kCGImageSourceShouldCache: @NO}), CFRelease);
    if (!source) throw std::invalid_argument("Invalid media artwork image");
    auto image_source = source.get();
    CFStringRef type = CGImageSourceGetType(image_source);
    if (!type || (!CFEqual(type, CFSTR("public.png")) && !CFEqual(type, CFSTR("public.jpeg"))))
        throw std::invalid_argument("Media artwork must be PNG or JPEG");
    NSDictionary* properties = CFBridgingRelease(CGImageSourceCopyPropertiesAtIndex(image_source, 0, nullptr));
    const double width = [properties[(__bridge NSString*)kCGImagePropertyPixelWidth] doubleValue];
    const double height = [properties[(__bridge NSString*)kCGImagePropertyPixelHeight] doubleValue];
    if (!std::isfinite(width) || !std::isfinite(height) || width < 1 || height < 1 ||
        width > 8192 || height > 8192 || width * height > 16 * 1024 * 1024)
        throw std::invalid_argument("Media artwork dimensions are too large or invalid");
    const std::unique_ptr<std::remove_pointer_t<CGImageRef>, decltype(&CFRelease)> image(
        CGImageSourceCreateThumbnailAtIndex(image_source, 0, (__bridge CFDictionaryRef)@{
            (__bridge NSString*)kCGImageSourceCreateThumbnailFromImageAlways: @YES,
            (__bridge NSString*)kCGImageSourceCreateThumbnailWithTransform: @YES,
            (__bridge NSString*)kCGImageSourceThumbnailMaxPixelSize: @1024,
            (__bridge NSString*)kCGImageSourceShouldCacheImmediately: @YES,
        }), CFRelease);
    if (!image) throw std::invalid_argument("Media artwork could not be decoded");
    NSImage* decoded = [[NSImage alloc] initWithCGImage:image.get() size:NSZeroSize];
    // The provider owns only the bounded immutable image, not this controller or its emitter.
    return [[MPMediaItemArtwork alloc] initWithBoundsSize:decoded.size
        requestHandler:^NSImage* (CGSize) { return decoded; }];
}

struct CallbackState final {
    std::mutex mutex;
    std::function<void(nlohmann::json)> emit;
    bool active = false;
    bool seekable = false;
    bool playing = false;
    std::uint64_t generation = 0;
    std::int64_t duration_ms = 0;

    void invalidate() {
        std::lock_guard lock(mutex);
        active = false;
        seekable = false;
        ++generation;
    }

    MPRemoteCommandHandlerStatus command(std::uint64_t expected, const char* action,
                                         MPRemoteCommandEvent* event) noexcept {
        try {
            // Serializes emitter use with withdrawal/destruction. No AppKit calls under this lock.
            std::lock_guard lock(mutex);
            if (!active || generation != expected) return MPRemoteCommandHandlerStatusNoActionableNowPlayingItem;
            if (std::string_view(action) == "toggle") action = playing ? "pause" : "play";
            nlohmann::json message{{"event", "transport"}, {"action", action}};
            if (std::string_view(action) == "seek") {
                if (!seekable || ![event isKindOfClass:[MPChangePlaybackPositionCommandEvent class]])
                    return MPRemoteCommandHandlerStatusCommandFailed;
                const double seconds = ((MPChangePlaybackPositionCommandEvent*)event).positionTime;
                if (!std::isfinite(seconds) || seconds < 0)
                    return MPRemoteCommandHandlerStatusCommandFailed;
                // Clamp in floating point before conversion, including huge finite OS requests.
                const double millis = std::min(seconds * 1000.0, static_cast<double>(duration_ms));
                const auto position = millis >= static_cast<double>(duration_ms)
                    ? duration_ms : static_cast<std::int64_t>(millis);
                message["position_ms"] = position;
            }
            emit(std::move(message));
            return MPRemoteCommandHandlerStatusSuccess;
        } catch (...) {
            return MPRemoteCommandHandlerStatusCommandFailed;
        }
    }
};

struct CommandTarget final {
    MPRemoteCommand* __strong command;
    id __strong token;
};

} // namespace

struct MacMediaControls::Impl final : std::enable_shared_from_this<Impl> {
    explicit Impl(std::function<void(nlohmann::json)> emit) : callback(std::make_shared<CallbackState>()) {
        if (!emit) throw std::invalid_argument("Media controls emitter is required");
        callback->emit = std::move(emit);
    }

    // Only pending and CallbackState are touched off the application main thread.
    std::mutex pending_mutex;
    std::optional<nlohmann::json> pending;
    bool posted = false;
    bool accepting = true;
    std::shared_ptr<CallbackState> callback;
    std::vector<CommandTarget> targets;
    std::uint64_t target_generation = 0;
    NSMutableDictionary* __strong metadata = nil;
    std::string cached_title, cached_artist, cached_album, cached_artwork;

    void enqueue(std::optional<nlohmann::json> state) {
        std::lock_guard lock(pending_mutex);
        if (!accepting) return;
        if (!state || !state->value("enabled", false) || state->value("state", "stopped") == "stopped")
            callback->invalidate();
        pending = std::move(state);
        if (posted) return;
        posted = true;
        const auto self = shared_from_this();
        dispatch_async(dispatch_get_main_queue(), ^{ self->drain(); });
    }

    void error() noexcept {
        try {
            callback->emit({{"event", "media_controls_error"},
                {"error", {{"code", "media_controls_unavailable"},
                    {"message", "macOS media controls could not be updated."}}}});
        } catch (...) {}
    }

    void drain() noexcept {
        @autoreleasepool {
            std::optional<nlohmann::json> state;
            std::uint64_t generation;
            {
                std::lock_guard lock(pending_mutex);
                if (!accepting) return;
                state = std::move(pending);
                pending.reset();
                posted = false;
                std::lock_guard callback_lock(callback->mutex);
                generation = callback->generation;
            }
            try {
                @try {
                    if (state && state->value("enabled", false) && state->value("state", "stopped") != "stopped")
                        apply(*state, generation);
                    else withdraw();
                    return;
                } @catch (NSException* exception) { (void)exception; }
            } catch (...) {}
            withdraw_noexcept();
            error();
        }
    }

    void add(MPRemoteCommand* command, const char* action, std::uint64_t generation) {
        const auto state = callback;
        id token = [command addTargetWithHandler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent* event) {
            @try { return state->command(generation, action, event); }
            @catch (NSException* exception) { (void)exception; return MPRemoteCommandHandlerStatusCommandFailed; }
        }];
        if (!token) throw std::runtime_error("Media command registration failed");
        targets.push_back({command, token});
        command.enabled = YES;
    }

    void apply(const nlohmann::json& state, std::uint64_t generation) {
        {
            std::lock_guard lock(callback->mutex);
            if (callback->generation != generation) return;
        }
        MPNowPlayingInfoCenter* center = [MPNowPlayingInfoCenter defaultCenter];
        MPRemoteCommandCenter* remote = [MPRemoteCommandCenter sharedCommandCenter];
        if (!center || !remote) throw std::runtime_error("MediaPlayer is unavailable");
        const auto& title = state.at("title").get_ref<const std::string&>();
        const auto& artist = state.at("artist").get_ref<const std::string&>();
        const auto& album = state.at("album").get_ref<const std::string&>();
        static const std::string empty;
        const auto& image = state.contains("artwork_base64") ? state.at("artwork_base64").get_ref<const std::string&>() : empty;
        if (!metadata) metadata = [NSMutableDictionary dictionary];
        if (!metadata[MPMediaItemPropertyTitle] || title != cached_title) {
            metadata[MPMediaItemPropertyTitle] = text(title);
            cached_title = title;
        }
        if (!metadata[MPMediaItemPropertyArtist] || artist != cached_artist) {
            metadata[MPMediaItemPropertyArtist] = text(artist);
            cached_artist = artist;
        }
        if (!metadata[MPMediaItemPropertyAlbumTitle] || album != cached_album) {
            metadata[MPMediaItemPropertyAlbumTitle] = text(album);
            cached_album = album;
        }
        if (image != cached_artwork) {
            MPMediaItemArtwork* decoded = artwork(image);
            if (decoded) metadata[MPMediaItemPropertyArtwork] = decoded;
            else [metadata removeObjectForKey:MPMediaItemPropertyArtwork];
            cached_artwork = image;
        }
        const auto duration = state.at("duration_ms").get<std::int64_t>();
        const auto position = state.at("position_ms").get<std::int64_t>();
        const bool playing = state.at("state") == "playing";
        const bool seekable = state.value("seekable", false) && duration > 0;
        metadata[MPMediaItemPropertyPlaybackDuration] = @(static_cast<double>(duration) / 1000.0);
        metadata[MPNowPlayingInfoPropertyElapsedPlaybackTime] = @(static_cast<double>(duration > 0 ? std::min(position, duration) : position) / 1000.0);
        metadata[MPNowPlayingInfoPropertyPlaybackRate] = @(playing ? 1.0 : 0.0);
        metadata[MPNowPlayingInfoPropertyDefaultPlaybackRate] = @1.0;
        metadata[MPNowPlayingInfoPropertyMediaType] = @(MPNowPlayingInfoMediaTypeAudio);
        {
            std::lock_guard lock(callback->mutex);
            callback->seekable = seekable;
            callback->playing = playing;
            callback->duration_ms = duration;
        }
        if (!targets.empty() && target_generation != generation) {
            for (const auto& target : targets) {
                target.command.enabled = NO;
                [target.command removeTarget:target.token];
            }
            targets.clear();
        }
        if (targets.empty()) {
            targets.reserve(7);
            target_generation = generation;
            add(remote.playCommand, "play", generation);
            add(remote.pauseCommand, "pause", generation);
            add(remote.togglePlayPauseCommand, "toggle", generation);
            add(remote.stopCommand, "stop", generation);
            add(remote.nextTrackCommand, "next", generation);
            add(remote.previousTrackCommand, "previous", generation);
            add(remote.changePlaybackPositionCommand, "seek", generation);
        }
        remote.changePlaybackPositionCommand.enabled = seekable;
        center.nowPlayingInfo = metadata;
        // On macOS playbackState is required for remote commands as well as presentation.
        center.playbackState = playing ? MPNowPlayingPlaybackStatePlaying : MPNowPlayingPlaybackStatePaused;
        {
            std::lock_guard lock(callback->mutex);
            if (callback->generation == generation) callback->active = true;
        }
    }

    void withdraw() {
        callback->invalidate();
        for (const auto& target : targets) {
            target.command.enabled = NO;
            [target.command removeTarget:target.token];
        }
        targets.clear();
        MPNowPlayingInfoCenter* center = [MPNowPlayingInfoCenter defaultCenter];
        center.playbackState = MPNowPlayingPlaybackStateStopped;
        center.nowPlayingInfo = nil;
        metadata = nil;
        cached_title.clear();
        cached_artist.clear();
        cached_album.clear();
        cached_artwork.clear();
    }

    void withdraw_noexcept() noexcept {
        try { @try { withdraw(); } @catch (NSException* exception) { (void)exception; } } catch (...) {}
    }

    void close() {
        {
            std::lock_guard lock(pending_mutex);
            accepting = false;
            pending.reset();
        }
        callback->invalidate();
        const auto self = shared_from_this();
        // The app loop outlives run_protocol, including all controller destructors.
        if ([NSThread isMainThread]) withdraw_noexcept();
        else dispatch_sync(dispatch_get_main_queue(), ^{ @autoreleasepool { self->withdraw_noexcept(); } });
    }
};

MacMediaControls::MacMediaControls(std::function<void(nlohmann::json)> emit)
    : impl_(std::make_shared<Impl>(std::move(emit))) {}
MacMediaControls::~MacMediaControls() { impl_->close(); }
void MacMediaControls::update(const nlohmann::json& state) { impl_->enqueue(state); }
void MacMediaControls::clear() { impl_->enqueue(std::nullopt); }

int run_macos_audio_application(int (*run_protocol)()) {
    @autoreleasepool {
        NSApplication* app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyAccessory];
        [app finishLaunching];
        int result = 1;
        std::thread protocol([&] {
            @autoreleasepool {
                try { result = run_protocol(); } catch (...) { result = 1; }
            }
            dispatch_async(dispatch_get_main_queue(), ^{
                [app stop:nil];
                // stop: alone does not wake AppKit's blocking nextEventMatchingMask:.
                [app postEvent:[NSEvent otherEventWithType:NSEventTypeApplicationDefined
                    location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil
                    subtype:0 data1:0 data2:0] atStart:YES];
            });
        });
        [app run];
        protocol.join();
        return result;
    }
}

} // namespace jastreamer
