mod journal;

pub use journal::{
    CommandDecision, DurableJournal, JournalCommand, JournalError, JournalResult, ProtocolFailure,
    payload_digest, stable_result_id,
};
use std::fmt;

#[derive(Clone, PartialEq, Eq, zeroize::Zeroize, zeroize::ZeroizeOnDrop)]
pub struct SecretToken(String);

impl SecretToken {
    pub fn parse(input: &str) -> Result<Self, TokenError> {
        let value = input.trim();
        if value.is_empty() || value.chars().any(char::is_control) {
            return Err(TokenError::Invalid);
        }
        Ok(Self(value.to_owned()))
    }

    pub fn expose(&self) -> &str {
        &self.0
    }
}

impl fmt::Debug for SecretToken {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("SecretToken([REDACTED])")
    }
}

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum TokenError {
    #[error("TOKEN_INVALID: bearer input is empty or contains control characters")]
    Invalid,
}
