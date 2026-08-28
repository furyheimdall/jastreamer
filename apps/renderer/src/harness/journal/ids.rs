use serde::Serialize;
use sha2::{Digest, Sha256};

pub fn payload_digest<T: Serialize>(value: &T) -> Result<String, serde_json::Error> {
    let bytes = serde_json::to_vec(value)?;
    Ok(hex::encode(Sha256::digest(bytes)))
}

pub fn stable_result_id(command_id: &str) -> String {
    let digest = Sha256::digest(command_id.as_bytes());
    format!("renderer-result-{}", hex::encode(&digest[..16]))
}
