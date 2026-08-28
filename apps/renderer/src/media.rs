use crate::harness::SecretToken;
use crate::protocol::MediaRepresentation;
use std::sync::{Arc, Mutex};
use std::time::Duration;
use symphonia::core::errors::Error as SymphoniaError;
use ureq::Agent;
use ureq::tls::{Certificate, RootCerts, TlsConfig, TlsProvider};
use url::Url;

pub use crate::media_http::RangeMediaSource;
pub use crate::media_stream::{
    AudioStream, PcmFormat, PcmProducer, StreamItem, StreamSendError, pcm_channel,
};

pub trait MediaLoader {
    fn open(
        &self,
        media: &MediaRepresentation,
        position_ms: u64,
    ) -> Result<AudioStream, MediaError>;
}

#[derive(Debug)]
pub struct PinnedHttpMediaLoader {
    server_origin: Url,
    token: SecretToken,
    server_certificate_der: Arc<Mutex<Option<Vec<u8>>>>,
}

impl PinnedHttpMediaLoader {
    pub fn new(
        server_origin: Url,
        token: SecretToken,
        server_certificate_der: Arc<Mutex<Option<Vec<u8>>>>,
    ) -> Self {
        Self {
            server_origin,
            token,
            server_certificate_der,
        }
    }

    fn client(&self) -> Result<Agent, MediaError> {
        let certificate = self
            .server_certificate_der
            .lock()
            .map_err(|_| MediaError::Authentication("certificate lock failed".to_owned()))?
            .clone()
            .ok_or_else(|| {
                MediaError::Authentication("Server certificate was not observed".to_owned())
            })?;
        let certificate = Certificate::from_der(&certificate).to_owned();
        Ok(Agent::config_builder()
            .max_redirects(0)
            .timeout_connect(Some(Duration::from_secs(10)))
            .timeout_recv_response(Some(Duration::from_secs(10)))
            .timeout_recv_body(Some(Duration::from_secs(3)))
            .http_status_as_error(false)
            .tls_config(
                TlsConfig::builder()
                    .provider(TlsProvider::Rustls)
                    .unversioned_rustls_crypto_provider(Arc::new(
                        rustls::crypto::ring::default_provider(),
                    ))
                    .root_certs(RootCerts::new_with_certs(&[certificate]))
                    .build(),
            )
            .build()
            .into())
    }
}

impl MediaLoader for PinnedHttpMediaLoader {
    fn open(
        &self,
        media: &MediaRepresentation,
        position_ms: u64,
    ) -> Result<AudioStream, MediaError> {
        ensure_supported(&media.mime_type)?;
        let media_url = parse_media_url(&self.server_origin, media)?;
        let (source, integrity) = crate::media_http::RangeMediaSource::open(
            self.client()?,
            media_url,
            self.token.clone(),
        )?;
        crate::decoder::decode_source(crate::decoder::DecodeRequest {
            source: Box::new(source),
            mime_type: &media.mime_type,
            position_ms,
            integrity: Some(integrity),
        })
    }
}

#[derive(Debug, thiserror::Error)]
pub enum MediaError {
    #[error("UNSUPPORTED_MEDIA: {0}")]
    Unsupported(String),
    #[error("MEDIA_AUTH_FAILED: media URL is outside configured Server origin")]
    OriginMismatch,
    #[error("MEDIA_AUTH_FAILED: {0}")]
    Authentication(String),
    #[error("MEDIA_CHANGED: media response is truncated or invalid")]
    Truncated,
    #[error("PLAYBACK_FAILED: decoder was cancelled")]
    Cancelled,
    #[error("PLAYBACK_FAILED: {0}")]
    Decode(String),
}

pub fn decode_media_source(
    source: Box<dyn symphonia::core::io::MediaSource>,
    mime_type: &str,
    position_ms: u64,
) -> Result<AudioStream, MediaError> {
    ensure_supported(mime_type)?;
    crate::decoder::decode_source(crate::decoder::DecodeRequest {
        source,
        mime_type,
        position_ms,
        integrity: None,
    })
}

fn parse_media_url(server_origin: &Url, media: &MediaRepresentation) -> Result<Url, MediaError> {
    let media_url = media
        .url
        .parse::<Url>()
        .map_err(|error| MediaError::Decode(error.to_string()))?;
    if media_url.scheme() != "https"
        || media_url.scheme() != server_origin.scheme()
        || media_url.host_str() != server_origin.host_str()
        || media_url.port_or_known_default() != server_origin.port_or_known_default()
    {
        return Err(MediaError::OriginMismatch);
    }
    if media.headers.keys().any(|name| {
        name.eq_ignore_ascii_case("authorization") || name.eq_ignore_ascii_case("cookie")
    }) {
        return Err(MediaError::Authentication(
            "Server supplied forbidden credential headers".to_owned(),
        ));
    }
    Ok(media_url)
}

fn ensure_supported(mime_type: &str) -> Result<(), MediaError> {
    match mime_type.split(';').next().map(str::trim) {
        Some(
            "audio/flac" | "audio/mpeg" | "audio/ogg" | "audio/opus" | "audio/vorbis" | "audio/wav"
            | "audio/x-wav",
        ) => Ok(()),
        _ => Err(MediaError::Unsupported(mime_type.to_owned())),
    }
}

pub(crate) fn extension_for_mime(mime_type: &str) -> Option<&'static str> {
    match mime_type.as_bytes() {
        b"audio/flac" => Some("flac"),
        b"audio/mpeg" => Some("mp3"),
        b"audio/ogg" | b"audio/opus" | b"audio/vorbis" => Some("ogg"),
        b"audio/wav" | b"audio/x-wav" => Some("wav"),
        _ => None,
    }
}

pub(crate) fn classify_decode_error(error: SymphoniaError) -> MediaError {
    match error {
        SymphoniaError::Unsupported(message) => MediaError::Unsupported(message.to_owned()),
        SymphoniaError::IoError(_) | SymphoniaError::DecodeError(_) => MediaError::Truncated,
        other => MediaError::Decode(other.to_string()),
    }
}
