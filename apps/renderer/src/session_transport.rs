use crate::harness::SecretToken;
use crate::protocol::{ServerFrame, decode_server_frame};
use crate::security::{CertificateFingerprint, PinnedCertificateVerifier};
use futures_util::StreamExt;
use rustls::ClientConfig;
use std::sync::{Arc, Mutex};
use tokio_tungstenite::Connector;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::http::HeaderValue;
use tokio_tungstenite::tungstenite::protocol::Message;
use url::Url;

pub type SessionSocket =
    tokio_tungstenite::WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>;

pub struct ConnectRequest<'a> {
    pub server_origin: &'a Url,
    pub fingerprint: CertificateFingerprint,
    pub renderer_id: &'a str,
    pub token: &'a SecretToken,
    pub observed_certificate: Arc<Mutex<Option<Vec<u8>>>>,
}

pub async fn connect(
    request: ConnectRequest<'_>,
) -> Result<SessionSocket, super::session::SessionError> {
    let verifier =
        PinnedCertificateVerifier::new(request.fingerprint, request.observed_certificate);
    let tls = ClientConfig::builder()
        .dangerous()
        .with_custom_certificate_verifier(Arc::new(verifier))
        .with_no_client_auth();
    let mut endpoint = request.server_origin.clone();
    endpoint.set_scheme("wss").map_err(|()| {
        super::session::SessionError::Invalid("Server origin must be HTTPS".to_owned())
    })?;
    endpoint.set_path(&format!(
        "/api/v1/renderers/{}/session",
        request.renderer_id
    ));
    endpoint.set_query(None);
    let mut upgrade = endpoint.as_str().into_client_request()?;
    upgrade.headers_mut().insert(
        "Authorization",
        HeaderValue::from_str(&format!("Bearer {}", request.token.expose()))
            .map_err(|error| super::session::SessionError::Invalid(error.to_string()))?,
    );
    upgrade.headers_mut().insert(
        "Sec-WebSocket-Protocol",
        HeaderValue::from_static("jastreamer.renderer.v3"),
    );
    let (socket, response) = tokio_tungstenite::connect_async_tls_with_config(
        upgrade,
        None,
        true,
        Some(Connector::Rustls(Arc::new(tls))),
    )
    .await?;
    let selected = response
        .headers()
        .get("Sec-WebSocket-Protocol")
        .and_then(|value| value.to_str().ok());
    if selected != Some("jastreamer.renderer.v3") {
        return Err(super::session::SessionError::Invalid(
            "Server did not select Renderer protocol v3".to_owned(),
        ));
    }
    Ok(socket)
}

pub async fn read_frame(
    socket: &mut SessionSocket,
) -> Result<ServerFrame, super::session::SessionError> {
    loop {
        let message = socket.next().await.ok_or_else(|| {
            super::session::SessionError::Invalid("Server closed session".to_owned())
        })??;
        match message {
            Message::Text(text) => return Ok(decode_server_frame(text.as_bytes())?),
            Message::Binary(bytes) => return Ok(decode_server_frame(&bytes)?),
            Message::Ping(_) | Message::Pong(_) | Message::Frame(_) => {}
            Message::Close(_) => {
                return Err(super::session::SessionError::Invalid(
                    "Server closed session".to_owned(),
                ));
            }
        }
    }
}
