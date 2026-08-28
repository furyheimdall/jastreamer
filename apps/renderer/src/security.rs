use rustls::client::danger::{HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier};
use rustls::crypto::{CryptoProvider, verify_tls12_signature, verify_tls13_signature};
use rustls::pki_types::{CertificateDer, ServerName, UnixTime};
use rustls::{DigitallySignedStruct, Error as RustlsError, SignatureScheme};
use sha2::{Digest, Sha256};
use std::sync::{Arc, Mutex};

#[derive(Clone, Copy, PartialEq, Eq)]
pub struct CertificateFingerprint([u8; 32]);

impl CertificateFingerprint {
    pub fn parse(input: &str) -> Result<Self, FingerprintError> {
        let normalized = input
            .trim()
            .strip_prefix("sha256:")
            .or_else(|| input.trim().strip_prefix("SHA256:"))
            .unwrap_or(input.trim())
            .replace(':', "");
        let bytes = hex::decode(&normalized).map_err(|_| FingerprintError::Invalid)?;
        let value: [u8; 32] = bytes.try_into().map_err(|_| FingerprintError::Invalid)?;
        Ok(Self(value))
    }

    pub fn matches_der(&self, certificate: &[u8]) -> bool {
        Sha256::digest(certificate).as_slice() == self.0
    }
}

impl std::fmt::Debug for CertificateFingerprint {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str("CertificateFingerprint(")?;
        formatter.write_str(&hex::encode(self.0))?;
        formatter.write_str(")")
    }
}

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum FingerprintError {
    #[error("FINGERPRINT_INVALID: expected 32-byte SHA-256 fingerprint")]
    Invalid,
}

#[derive(Debug)]
pub struct PinnedCertificateVerifier {
    fingerprint: CertificateFingerprint,
    provider: Arc<CryptoProvider>,
    observed_der: Arc<Mutex<Option<Vec<u8>>>>,
}

impl PinnedCertificateVerifier {
    pub fn new(
        fingerprint: CertificateFingerprint,
        observed_der: Arc<Mutex<Option<Vec<u8>>>>,
    ) -> Self {
        Self {
            fingerprint,
            provider: Arc::new(rustls::crypto::ring::default_provider()),
            observed_der,
        }
    }
}

impl ServerCertVerifier for PinnedCertificateVerifier {
    fn verify_server_cert(
        &self,
        end_entity: &CertificateDer<'_>,
        _intermediates: &[CertificateDer<'_>],
        _server_name: &ServerName<'_>,
        _ocsp_response: &[u8],
        _now: UnixTime,
    ) -> Result<ServerCertVerified, RustlsError> {
        if !self.fingerprint.matches_der(end_entity.as_ref()) {
            return Err(RustlsError::General(
                "advertised certificate fingerprint mismatch".to_owned(),
            ));
        }
        let mut observed = self
            .observed_der
            .lock()
            .map_err(|_| RustlsError::General("certificate observation lock failed".to_owned()))?;
        *observed = Some(end_entity.as_ref().to_vec());
        Ok(ServerCertVerified::assertion())
    }

    fn verify_tls12_signature(
        &self,
        message: &[u8],
        certificate: &CertificateDer<'_>,
        signature: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, RustlsError> {
        verify_tls12_signature(
            message,
            certificate,
            signature,
            &self.provider.signature_verification_algorithms,
        )
    }

    fn verify_tls13_signature(
        &self,
        message: &[u8],
        certificate: &CertificateDer<'_>,
        signature: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, RustlsError> {
        verify_tls13_signature(
            message,
            certificate,
            signature,
            &self.provider.signature_verification_algorithms,
        )
    }

    fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
        self.provider
            .signature_verification_algorithms
            .supported_schemes()
    }
}
