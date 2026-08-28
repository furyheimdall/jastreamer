use crate::decoder::StreamIntegrity;
use crate::harness::SecretToken;
use crate::media::MediaError;
use std::io::{self, Read, Seek, SeekFrom};
use ureq::Agent;
use url::Url;

const HTTP_RANGE_BYTES: u64 = 256 * 1024;
const MAX_MEDIA_BYTES: u64 = 512 * 1024 * 1024;

pub struct RangeMediaSource {
    requester: RangeRequester,
    response: ureq::http::Response<ureq::Body>,
    response_end: u64,
    position: u64,
    length: u64,
    integrity: StreamIntegrity,
}

impl RangeMediaSource {
    pub(crate) fn open(
        agent: Agent,
        url: Url,
        token: SecretToken,
    ) -> Result<(Self, StreamIntegrity), MediaError> {
        let requester = RangeRequester { agent, url, token };
        let (response, range) = requester.send(0)?;
        if range.total > MAX_MEDIA_BYTES {
            return Err(MediaError::Unsupported(
                "media exceeds streaming size boundary".to_owned(),
            ));
        }
        let integrity = StreamIntegrity::default();
        Ok((
            Self {
                requester,
                response,
                response_end: range.end,
                position: 0,
                length: range.total,
                integrity: integrity.clone(),
            },
            integrity,
        ))
    }

    pub fn open_untracked(agent: Agent, url: Url, token: SecretToken) -> Result<Self, MediaError> {
        Self::open(agent, url, token).map(|(source, _)| source)
    }

    fn restart(&mut self, position: u64) -> io::Result<u64> {
        if position > self.length {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "seek exceeds media length",
            ));
        }
        if position == self.length {
            self.position = position;
            return Ok(position);
        }
        let (response, range) = self.requester.send(position).map_err(io::Error::other)?;
        if range.total != self.length {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "media length changed between Range requests",
            ));
        }
        self.response = response;
        self.response_end = range.end;
        self.integrity.clear();
        self.position = position;
        Ok(position)
    }
}

impl Read for RangeMediaSource {
    fn read(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
        loop {
            let count = self.response.body_mut().as_reader().read(buffer)?;
            if count > 0 {
                self.position = self.position.saturating_add(count as u64);
                if self.position > self.response_end {
                    return Err(io::Error::new(
                        io::ErrorKind::InvalidData,
                        "media Range body exceeds Content-Range",
                    ));
                }
                if self.position == self.length {
                    self.integrity.mark_complete();
                }
                return Ok(count);
            }
            if self.position < self.response_end {
                return Err(io::Error::new(
                    io::ErrorKind::UnexpectedEof,
                    "media response ended before Content-Range",
                ));
            }
            if self.position == self.length {
                return Ok(0);
            }
            self.restart(self.position)?;
        }
    }
}

impl Seek for RangeMediaSource {
    fn seek(&mut self, position: SeekFrom) -> io::Result<u64> {
        let target = match position {
            SeekFrom::Start(value) => value,
            SeekFrom::Current(delta) => {
                self.position.checked_add_signed(delta).ok_or_else(|| {
                    io::Error::new(io::ErrorKind::InvalidInput, "media seek overflow")
                })?
            }
            SeekFrom::End(delta) => self.length.checked_add_signed(delta).ok_or_else(|| {
                io::Error::new(io::ErrorKind::InvalidInput, "media seek overflow")
            })?,
        };
        if target == self.position {
            return Ok(target);
        }
        self.restart(target)
    }
}

impl symphonia::core::io::MediaSource for RangeMediaSource {
    fn is_seekable(&self) -> bool {
        true
    }

    fn byte_len(&self) -> Option<u64> {
        Some(self.length)
    }
}

struct RangeRequester {
    agent: Agent,
    url: Url,
    token: SecretToken,
}

impl RangeRequester {
    fn send(
        &self,
        position: u64,
    ) -> Result<(ureq::http::Response<ureq::Body>, ResponseRange), MediaError> {
        let end = position.saturating_add(HTTP_RANGE_BYTES - 1);
        let response = self
            .agent
            .get(self.url.as_str())
            .header("Authorization", &format!("Bearer {}", self.token.expose()))
            .header("Range", &format!("bytes={position}-{end}"))
            .call()
            .map_err(|error| MediaError::Authentication(error.to_string()))?;
        let status = response.status().as_u16();
        if status == 401 || status == 403 {
            return Err(MediaError::Authentication(format!(
                "Server returned {status}"
            )));
        }
        if status != 206 {
            return Err(MediaError::Decode(format!(
                "media Range response status {status}"
            )));
        }
        let range = parse_content_range(&response, position)?;
        Ok((response, range))
    }
}

struct ResponseRange {
    end: u64,
    total: u64,
}

fn parse_content_range(
    response: &ureq::http::Response<ureq::Body>,
    expected_start: u64,
) -> Result<ResponseRange, MediaError> {
    let value = response
        .headers()
        .get("Content-Range")
        .ok_or_else(|| MediaError::Decode("Content-Range is required".to_owned()))?
        .to_str()
        .map_err(|error| MediaError::Decode(error.to_string()))?;
    let value = value
        .strip_prefix("bytes ")
        .ok_or_else(|| MediaError::Decode("invalid Content-Range unit".to_owned()))?;
    let (bounds, total) = value
        .split_once('/')
        .ok_or_else(|| MediaError::Decode("invalid Content-Range".to_owned()))?;
    let (start, end) = bounds
        .split_once('-')
        .ok_or_else(|| MediaError::Decode("invalid Content-Range bounds".to_owned()))?;
    let start = start
        .parse::<u64>()
        .map_err(|error| MediaError::Decode(error.to_string()))?;
    let end = end
        .parse::<u64>()
        .map_err(|error| MediaError::Decode(error.to_string()))?;
    let total = total
        .parse::<u64>()
        .map_err(|error| MediaError::Decode(error.to_string()))?;
    if start != expected_start || end < start || end >= total {
        return Err(MediaError::Decode("inconsistent Content-Range".to_owned()));
    }
    Ok(ResponseRange {
        end: end.saturating_add(1),
        total,
    })
}
