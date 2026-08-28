use crate::audio::AudioError;
use crate::harness::ProtocolFailure;
use crate::protocol::failure;

pub(super) fn audio_failure(error: &AudioError, kind: &str) -> ProtocolFailure {
    let text = error.to_string();
    let code = if text.contains("UNSUPPORTED_MEDIA") {
        "UNSUPPORTED_MEDIA"
    } else if text.contains("MEDIA_AUTH_FAILED") {
        "MEDIA_AUTH_FAILED"
    } else {
        match error {
            AudioError::NoEndpoint | AudioError::BusyEndpoint | AudioError::Invalidated => {
                "OUTPUT_UNAVAILABLE"
            }
            AudioError::UnsupportedFormat | AudioError::StartTimeout | AudioError::Failed(_) => {
                if kind == "unsupported" {
                    "UNSUPPORTED_COMMAND"
                } else {
                    "PLAYBACK_FAILED"
                }
            }
        }
    };
    failure(code, &text, matches!(error, AudioError::Invalidated))
}
