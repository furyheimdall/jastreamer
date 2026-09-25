#ifndef NOMINMAX
#define NOMINMAX
#endif
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <windows.h>

#include <shcore.h>
#include <shlwapi.h>
#include <systemmediatransportcontrolsinterop.h>

#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Media.h>
#include <winrt/Windows.Storage.Streams.h>
#include <winrt/base.h>

#include "smtc.hpp"

#include <algorithm>
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstddef>
#include <cstdint>
#include <exception>
#include <memory>
#include <limits>
#include <mutex>
#include <optional>
#include <stdexcept>
#include <string>
#include <string_view>
#include <thread>
#include <utility>
#include <vector>

namespace jastreamer {
namespace {

using winrt::Windows::Foundation::TimeSpan;
using winrt::Windows::Media::MediaPlaybackStatus;
using winrt::Windows::Media::MediaPlaybackType;
using winrt::Windows::Media::PlaybackPositionChangeRequestedEventArgs;
using winrt::Windows::Media::SystemMediaTransportControls;
using winrt::Windows::Media::SystemMediaTransportControlsButton;
using winrt::Windows::Media::SystemMediaTransportControlsButtonPressedEventArgs;
using winrt::Windows::Media::SystemMediaTransportControlsTimelineProperties;
using winrt::Windows::Storage::Streams::IRandomAccessStream;
using winrt::Windows::Storage::Streams::RandomAccessStreamReference;

constexpr UINT kDispatchMessage = WM_APP + 0x4a;
constexpr UINT kShutdownMessage = WM_APP + 0x4b;
constexpr std::size_t kMaxArtworkBytes = 192 * 1024;
constexpr std::int64_t kMaxTimelineMilliseconds =
    std::numeric_limits<std::int64_t>::max() / 10'000;
constexpr wchar_t kWindowClassName[] = L"JastreamerAudioSmtcWindow.8A374D91";

class CallbackState final {
public:
    explicit CallbackState(std::function<void(nlohmann::json)> emit)
        : emit_(std::move(emit)) {
        if (!emit_) {
            throw std::invalid_argument("SMTC transport emitter is required");
        }
    }

    void emit(nlohmann::json message, bool require_active = true) noexcept {
        {
            std::lock_guard lock(mutex_);
            if (!accepting_ || (require_active && !active_.load(std::memory_order_acquire))) {
                return;
            }
            ++in_flight_;
        }

        try {
            emit_(std::move(message));
        } catch (...) {
            // An event raised by Windows must never unwind through the ABI boundary.
        }

        {
            std::lock_guard lock(mutex_);
            --in_flight_;
            if (!accepting_ && in_flight_ == 0) {
                idle_.notify_all();
            }
        }
    }
    void set_active(bool active) noexcept {
        active_.store(active, std::memory_order_release);
    }

    bool active() const noexcept {
        return active_.load(std::memory_order_acquire);
    }

    void set_seekable(bool seekable) noexcept {
        seekable_.store(seekable, std::memory_order_release);
    }

    bool seekable() const noexcept {
        return seekable_.load(std::memory_order_acquire);
    }


    void shutdown() noexcept {
        std::unique_lock lock(mutex_);
        seekable_.store(false, std::memory_order_release);
        active_.store(false, std::memory_order_release);
        accepting_ = false;
        idle_.wait(lock, [this] { return in_flight_ == 0; });
        emit_ = {};
    }

private:
    std::mutex mutex_;
    std::condition_variable idle_;
    std::function<void(nlohmann::json)> emit_;
    std::atomic<bool> active_{false};
    std::atomic<bool> seekable_{false};
    std::size_t in_flight_ = 0;
    bool accepting_ = true;
};

int base64_value(char value) noexcept {
    if (value >= 'A' && value <= 'Z') {
        return value - 'A';
    }
    if (value >= 'a' && value <= 'z') {
        return value - 'a' + 26;
    }
    if (value >= '0' && value <= '9') {
        return value - '0' + 52;
    }
    if (value == '+') {
        return 62;
    }
    if (value == '/') {
        return 63;
    }
    return -1;
}

std::optional<std::vector<std::uint8_t>> decode_artwork(std::string_view encoded) {
    if (encoded.empty()) {
        return std::nullopt;
    }
    if (encoded.size() % 4 != 0 || encoded.size() > ((kMaxArtworkBytes + 2) / 3) * 4) {
        throw std::invalid_argument("artwork_base64 is invalid or too large");
    }

    std::size_t padding = 0;
    if (encoded.ends_with("=")) {
        padding = 1;
        if (encoded.size() >= 2 && encoded[encoded.size() - 2] == '=') {
            padding = 2;
        }
    }
    const auto decoded_size = (encoded.size() / 4) * 3 - padding;
    if (decoded_size > kMaxArtworkBytes) {
        throw std::invalid_argument("artwork_base64 is too large");
    }

    std::vector<std::uint8_t> decoded;
    decoded.reserve(decoded_size);
    for (std::size_t offset = 0; offset < encoded.size(); offset += 4) {
        const bool final_group = offset + 4 == encoded.size();
        const int first = base64_value(encoded[offset]);
        const int second = base64_value(encoded[offset + 1]);
        const int third = encoded[offset + 2] == '=' ? -1 : base64_value(encoded[offset + 2]);
        const int fourth = encoded[offset + 3] == '=' ? -1 : base64_value(encoded[offset + 3]);

        if (first < 0 || second < 0 || (!final_group && (third < 0 || fourth < 0))) {
            throw std::invalid_argument("artwork_base64 contains invalid data");
        }
        if (third < 0) {
            if (!final_group || encoded[offset + 2] != '=' || encoded[offset + 3] != '=' ||
                (second & 0x0f) != 0) {
                throw std::invalid_argument("artwork_base64 has invalid padding");
            }
        } else if (fourth < 0) {
            if (!final_group || encoded[offset + 3] != '=' || (third & 0x03) != 0) {
                throw std::invalid_argument("artwork_base64 has invalid padding");
            }
        }

        decoded.push_back(static_cast<std::uint8_t>((first << 2) | (second >> 4)));
        if (third >= 0) {
            decoded.push_back(static_cast<std::uint8_t>((second << 4) | (third >> 2)));
        }
        if (fourth >= 0) {
            decoded.push_back(static_cast<std::uint8_t>((third << 6) | fourth));
        }
    }

    return decoded;
}

RandomAccessStreamReference artwork_reference(const std::vector<std::uint8_t>& bytes) {
    winrt::com_ptr<IStream> memory_stream;
    memory_stream.attach(SHCreateMemStream(bytes.data(), static_cast<UINT>(bytes.size())));
    if (!memory_stream) {
        throw winrt::hresult_error(E_OUTOFMEMORY);
    }

    IRandomAccessStream random_access_stream{nullptr};
    winrt::check_hresult(CreateRandomAccessStreamOverStream(
        memory_stream.get(), BSOS_DEFAULT, winrt::guid_of<IRandomAccessStream>(),
        winrt::put_abi(random_access_stream)));
    return RandomAccessStreamReference::CreateFromStream(random_access_stream);
}

std::string_view text_value(const nlohmann::json& state, const char* name) {
    const auto value = state.find(name);
    if (value == state.end() || value->is_null()) {
        return {};
    }
    if (!value->is_string()) {
        throw std::invalid_argument(std::string(name) + " must be a string");
    }
    return value->get_ref<const std::string&>();
}

TimeSpan milliseconds_to_timespan(std::int64_t milliseconds) noexcept {
    using HundredNanoseconds = std::chrono::duration<std::int64_t, std::ratio<1, 10'000'000>>;
    const auto bounded = std::clamp<std::int64_t>(milliseconds, 0, kMaxTimelineMilliseconds);
    return HundredNanoseconds{bounded * 10'000};
}

MediaPlaybackStatus playback_status(const std::string& state) {
    if (state == "playing") {
        return MediaPlaybackStatus::Playing;
    }
    if (state == "paused") {
        return MediaPlaybackStatus::Paused;
    }
    if (state == "changing") {
        return MediaPlaybackStatus::Changing;
    }
    if (state == "stopped") {
        return MediaPlaybackStatus::Stopped;
    }
    throw std::invalid_argument("unsupported SMTC playback state");
}

void emit_transport(const std::shared_ptr<CallbackState>& callback, std::string_view action) noexcept {
    try {
        callback->emit(
            nlohmann::json{{"event", "transport"}, {"action", std::string(action)}});
    } catch (...) {
    }
}

} // namespace

struct Smtc::Impl final {
    explicit Impl(std::function<void(nlohmann::json)> emit)
        : callback_(std::make_shared<CallbackState>(std::move(emit))) {
        thread_ = std::thread([this] { run(); });
        std::unique_lock lock(startup_mutex_);
        startup_ready_.wait(lock, [this] { return startup_complete_; });
        const auto error = startup_error_;
        lock.unlock();

        if (error) {
            if (thread_.joinable()) {
                thread_.join();
            }
            std::rethrow_exception(error);
        }
    }

    ~Impl() {
        callback_->shutdown();

        {
            std::lock_guard lock(pending_mutex_);
            accepting_ = false;
            has_pending_ = false;
            dispatch_posted_ = false;
            pending_state_.reset();
        }

        if (const HWND window = window_.load(std::memory_order_acquire)) {
            SendMessageW(window, kShutdownMessage, 0, 0);
        }
        if (thread_.joinable()) {
            thread_.join();
        }
    }

    void update(const nlohmann::json& state) {
        bool should_wake = false;
        {
            std::lock_guard lock(pending_mutex_);
            if (!accepting_) {
                return;
            }
            pending_state_ = state;
            has_pending_ = true;
            if (!dispatch_posted_) {
                dispatch_posted_ = true;
                should_wake = true;
            }
        }
        if (should_wake) {
            wake();
        }
    }

    void clear() {
        bool should_wake = false;
        {
            std::lock_guard lock(pending_mutex_);
            if (!accepting_) {
                return;
            }
            pending_state_.reset();
            has_pending_ = true;
            if (!dispatch_posted_) {
                dispatch_posted_ = true;
                should_wake = true;
            }
        }
        if (should_wake) {
            wake();
        }
    }

private:
    static LRESULT CALLBACK window_proc(HWND window, UINT message, WPARAM wparam, LPARAM lparam) noexcept {
        Impl* self = reinterpret_cast<Impl*>(GetWindowLongPtrW(window, GWLP_USERDATA));
        if (message == WM_NCCREATE) {
            const auto* create = reinterpret_cast<const CREATESTRUCTW*>(lparam);
            self = static_cast<Impl*>(create->lpCreateParams);
            SetWindowLongPtrW(window, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(self));
        }

        if (self) {
            if (message == kDispatchMessage) {
                self->drain();
                return 0;
            }
            if (message == kShutdownMessage) {
                PostQuitMessage(0);
                return 0;
            }
            if (message == WM_NCDESTROY) {
                self->window_.store(nullptr, std::memory_order_release);
                SetWindowLongPtrW(window, GWLP_USERDATA, 0);
            }
        }

        if (message == WM_DESTROY) {
            PostQuitMessage(0);
            return 0;
        }
        return DefWindowProcW(window, message, wparam, lparam);
    }

    static void register_window_class() {
        static std::once_flag once;
        std::call_once(once, [] {
            WNDCLASSEXW window_class{};
            window_class.cbSize = sizeof(window_class);
            window_class.lpfnWndProc = &Impl::window_proc;
            window_class.hInstance = GetModuleHandleW(nullptr);
            window_class.lpszClassName = kWindowClassName;
            if (!RegisterClassExW(&window_class) && GetLastError() != ERROR_CLASS_ALREADY_EXISTS) {
                winrt::throw_last_error();
            }
        });
    }

    void complete_startup(std::exception_ptr error = {}) noexcept {
        std::lock_guard lock(startup_mutex_);
        if (startup_complete_) {
            return;
        }
        startup_error_ = std::move(error);
        startup_complete_ = true;
        startup_ready_.notify_one();
    }

    void run() noexcept {
        bool apartment_initialized = false;
        HWND window = nullptr;
        try {
            winrt::init_apartment(winrt::apartment_type::multi_threaded);
            apartment_initialized = true;
            register_window_class();

            window = CreateWindowExW(
                WS_EX_NOACTIVATE | WS_EX_TOOLWINDOW, kWindowClassName, L"Jastreamer Audio",
                WS_OVERLAPPED, 0, 0, 0, 0, nullptr, nullptr, GetModuleHandleW(nullptr), this);
            if (!window) {
                winrt::throw_last_error();
            }
            window_.store(window, std::memory_order_release);

            initialize_controls(window);
            complete_startup();

            MSG message{};
            while (GetMessageW(&message, nullptr, 0, 0) > 0) {
                TranslateMessage(&message);
                DispatchMessageW(&message);
            }
        } catch (...) {
            complete_startup(std::current_exception());
        }

        withdraw_noexcept();
        revoke_events_noexcept();
        controls_ = nullptr;

        if (window && IsWindow(window)) {
            DestroyWindow(window);
        }
        window_.store(nullptr, std::memory_order_release);
        if (apartment_initialized) {
            winrt::uninit_apartment();
        }
    }

    void initialize_controls(HWND window) {
        const auto interop = winrt::get_activation_factory<
            SystemMediaTransportControls, ISystemMediaTransportControlsInterop>();
        winrt::check_hresult(interop->GetForWindow(
            window, winrt::guid_of<SystemMediaTransportControls>(), winrt::put_abi(controls_)));

        controls_.IsEnabled(false);
        set_buttons(false);

        const auto callback = callback_;
        button_token_ = controls_.ButtonPressed(
            [callback](const SystemMediaTransportControls&,
                       const SystemMediaTransportControlsButtonPressedEventArgs& args) noexcept {
                if (!callback->active()) {
                    return;
                }
                try {
                    switch (args.Button()) {
                    case SystemMediaTransportControlsButton::Play:
                        emit_transport(callback, "play");
                        break;
                    case SystemMediaTransportControlsButton::Pause:
                        emit_transport(callback, "pause");
                        break;
                    case SystemMediaTransportControlsButton::Stop:
                        emit_transport(callback, "stop");
                        break;
                    case SystemMediaTransportControlsButton::Next:
                        emit_transport(callback, "next");
                        break;
                    case SystemMediaTransportControlsButton::Previous:
                        emit_transport(callback, "previous");
                        break;
                    default:
                        break;
                    }
                } catch (...) {
                }
            });

        position_token_ = controls_.PlaybackPositionChangeRequested(
            [callback](const SystemMediaTransportControls&,
                       const PlaybackPositionChangeRequestedEventArgs& args) noexcept {
                if (!callback->active() || !callback->seekable()) {
                    return;
                }
                try {
                    const auto requested = std::chrono::duration_cast<std::chrono::milliseconds>(
                                               args.RequestedPlaybackPosition())
                                               .count();
                    callback->emit(nlohmann::json{
                        {"event", "transport"},
                        {"action", "seek"},
                        {"position_ms", std::max<std::int64_t>(0, requested)},
                    });
                } catch (...) {
                }
            });
    }

    void set_buttons(bool enabled) {
        controls_.IsPlayEnabled(enabled);
        controls_.IsPauseEnabled(enabled);
        controls_.IsStopEnabled(enabled);
        controls_.IsNextEnabled(enabled);
        controls_.IsPreviousEnabled(enabled);
        controls_.IsFastForwardEnabled(false);
        controls_.IsRewindEnabled(false);
        controls_.IsRecordEnabled(false);
        controls_.IsChannelUpEnabled(false);
        controls_.IsChannelDownEnabled(false);
    }

    void wake() noexcept {
        if (const HWND window = window_.load(std::memory_order_acquire)) {
            PostMessageW(window, kDispatchMessage, 0, 0);
        }
    }

    void drain() noexcept {
        for (;;) {
            std::optional<nlohmann::json> state;
            {
                std::lock_guard lock(pending_mutex_);
                if (!has_pending_) {
                    dispatch_posted_ = false;
                    return;
                }
                state = std::move(pending_state_);
                pending_state_.reset();
                has_pending_ = false;
            }

            try {
                if (state) {
                    apply(*state);
                } else {
                    withdraw();
                }
            } catch (...) {
                withdraw_noexcept();
                callback_->emit(nlohmann::json{
                    {"event", "smtc_error"},
                    {"error", {{"code", "smtc_unavailable"},
                               {"message", "Windows media controls could not be updated."}}},
                }, false);
            }
        }
    }

    void apply(const nlohmann::json& state) {
        if (!state.value("enabled", false)) {
            withdraw();
            return;
        }

        const auto duration = std::clamp<std::int64_t>(
            state.value("duration_ms", std::int64_t{0}), 0, kMaxTimelineMilliseconds);
        const auto position = std::clamp<std::int64_t>(
            state.value("position_ms", std::int64_t{0}), 0, duration);
        const auto status = playback_status(state.value("state", std::string{"stopped"}));
        const bool seekable = state.value("seekable", false);
        if (!seekable) {
            callback_->set_seekable(false);
        }

        const auto title = text_value(state, "title");
        const auto artist = text_value(state, "artist");
        const auto album = text_value(state, "album");
        const auto artwork_text = text_value(state, "artwork_base64");
        const bool metadata_changed =
            !metadata_initialized_ || cached_title_.compare(title) != 0 ||
            cached_artist_.compare(artist) != 0 || cached_album_.compare(album) != 0 ||
            cached_artwork_.compare(artwork_text) != 0;
        if (metadata_changed) {
            const auto artwork = decode_artwork(artwork_text);
            auto updater = controls_.DisplayUpdater();
            updater.ClearAll();
            updater.Type(MediaPlaybackType::Music);
            auto music = updater.MusicProperties();
            music.Title(winrt::to_hstring(title));
            music.Artist(winrt::to_hstring(artist));
            music.AlbumTitle(winrt::to_hstring(album));
            if (artwork) {
                updater.Thumbnail(artwork_reference(*artwork));
            } else {
                updater.Thumbnail(nullptr);
            }
            updater.Update();

            cached_title_.assign(title.begin(), title.end());
            cached_artist_.assign(artist.begin(), artist.end());
            cached_album_.assign(album.begin(), album.end());
            cached_artwork_.assign(artwork_text.begin(), artwork_text.end());
            metadata_initialized_ = true;
        }

        SystemMediaTransportControlsTimelineProperties timeline;
        timeline.StartTime(milliseconds_to_timespan(0));
        timeline.EndTime(milliseconds_to_timespan(duration));
        timeline.MinSeekTime(milliseconds_to_timespan(seekable ? 0 : position));
        timeline.MaxSeekTime(milliseconds_to_timespan(seekable ? duration : position));
        timeline.Position(milliseconds_to_timespan(position));
        controls_.UpdateTimelineProperties(timeline);

        set_buttons(true);
        controls_.PlaybackStatus(status);
        controls_.IsEnabled(true);
        callback_->set_seekable(seekable);
        callback_->set_active(true);
    }

    void withdraw() {
        callback_->set_active(false);
        callback_->set_seekable(false);
        metadata_initialized_ = false;
        cached_title_.clear();
        cached_artist_.clear();
        cached_album_.clear();
        cached_artwork_.clear();
        controls_.IsEnabled(false);
        controls_.PlaybackStatus(MediaPlaybackStatus::Closed);
        set_buttons(false);

        SystemMediaTransportControlsTimelineProperties timeline;
        timeline.StartTime(milliseconds_to_timespan(0));
        timeline.EndTime(milliseconds_to_timespan(0));
        timeline.MinSeekTime(milliseconds_to_timespan(0));
        timeline.MaxSeekTime(milliseconds_to_timespan(0));
        timeline.Position(milliseconds_to_timespan(0));
        controls_.UpdateTimelineProperties(timeline);

        auto updater = controls_.DisplayUpdater();
        updater.ClearAll();
        updater.Update();
    }

    void withdraw_noexcept() noexcept {
        if (!controls_) {
            return;
        }
        try {
            withdraw();
        } catch (...) {
            try {
                controls_.IsEnabled(false);
            } catch (...) {
            }
        }
    }

    void revoke_events_noexcept() noexcept {
        if (!controls_) {
            return;
        }
        try {
            if (button_token_.value != 0) {
                controls_.ButtonPressed(button_token_);
                button_token_ = {};
            }
        } catch (...) {
        }
        try {
            if (position_token_.value != 0) {
                controls_.PlaybackPositionChangeRequested(position_token_);
                position_token_ = {};
            }
        } catch (...) {
        }
    }

    std::shared_ptr<CallbackState> callback_;
    std::thread thread_;

    std::mutex startup_mutex_;
    std::condition_variable startup_ready_;
    std::exception_ptr startup_error_;
    bool startup_complete_ = false;

    std::mutex pending_mutex_;
    std::optional<nlohmann::json> pending_state_;
    bool has_pending_ = false;
    bool dispatch_posted_ = false;
    bool accepting_ = true;

    std::atomic<HWND> window_{nullptr};
    SystemMediaTransportControls controls_{nullptr};
    winrt::event_token button_token_{};
    winrt::event_token position_token_{};
    std::string cached_title_;
    std::string cached_artist_;
    std::string cached_album_;
    std::string cached_artwork_;
    bool metadata_initialized_ = false;
};

Smtc::Smtc(std::function<void(nlohmann::json)> emit)
    : impl_(std::make_unique<Impl>(std::move(emit))) {}

Smtc::~Smtc() = default;

void Smtc::update(const nlohmann::json& state) {
    impl_->update(state);
}

void Smtc::clear() {
    impl_->clear();
}

} // namespace jastreamer
