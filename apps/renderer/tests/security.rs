use jastreamer_renderer::harness::SecretToken;
use jastreamer_renderer::media::{MediaError, MediaLoader, PinnedHttpMediaLoader};
use jastreamer_renderer::protocol::MediaRepresentation;
use jastreamer_renderer::security::CertificateFingerprint;
use url::Url;

#[test]
fn mismatched_certificate_fingerprint_is_rejected() {
    // Given
    let fingerprint = CertificateFingerprint::parse(&"0".repeat(64)).expect("fingerprint parses");

    // When / Then
    assert!(!fingerprint.matches_der(b"different certificate"));
}

#[test]
fn bearer_is_never_sent_to_media_on_another_origin() {
    // Given
    let origin = Url::parse("https://server.example:8443").expect("origin parses");
    let token = SecretToken::parse("bearer-sentinel").expect("token parses");
    let media = MediaRepresentation {
        url: "https://attacker.example/media".to_owned(),
        mime_type: "audio/flac".to_owned(),
        headers: Default::default(),
        seekable: true,
    };

    let loader = PinnedHttpMediaLoader::new(
        origin,
        token,
        std::sync::Arc::new(std::sync::Mutex::new(None)),
    );

    // When
    let result = loader.open(&media, 0);

    // Then
    assert!(matches!(result, Err(MediaError::OriginMismatch)));
}
