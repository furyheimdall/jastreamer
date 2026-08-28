use jastreamer_wasapi_loopback_probe::{
    FrequencyComparison, analyze, dominance_latency_ms, frequency_rejection_db,
};
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::path::Path;

#[derive(Debug, thiserror::Error)]
enum ProbeError {
    #[error(
        "USAGE: capture <output-f32le> <frame-count> <endpoint-sha256> <capture-id> | hold-exclusive <endpoint-sha256> <id> | analyze <input-f32le>"
    )]
    Usage,
    #[error("IO_ERROR: {0}")]
    Io(#[from] std::io::Error),
    #[error("JSON_ERROR: {0}")]
    Json(#[from] serde_json::Error),
    #[error("CAPTURE_BUFFER_INVALID")]
    InvalidBuffer,
    #[cfg(windows)]
    #[error(transparent)]
    Capture(#[from] jastreamer_wasapi_loopback_probe::wasapi::CaptureError),
}

#[derive(Serialize)]
struct AnalysisReceipt {
    capture_sha256: String,
    encoding: &'static str,
    sample_rate_hz: u32,
    channels: u16,
    metrics: jastreamer_wasapi_loopback_probe::SignalMetrics,
    rejection_db: f64,
    dominance_latency_ms: Option<u64>,
}

#[derive(Clone, Copy)]
struct CaptureArgs<'a> {
    output: &'a Path,
    frame_count: usize,
    endpoint_hash: &'a str,
    id: &'a str,
}

fn main() {
    if let Err(error) = run() {
        eprintln!("{error}");
        std::process::exit(65);
    }
}

fn run() -> Result<(), ProbeError> {
    let arguments: Vec<String> = std::env::args().skip(1).collect();
    match arguments.as_slice() {
        [command, output, frames, endpoint_hash, id] if command == "capture" => {
            capture(CaptureArgs {
                output: Path::new(output),
                frame_count: frames.parse().map_err(|_| ProbeError::Usage)?,
                endpoint_hash,
                id,
            })
        }
        [command, endpoint_hash, id] if command == "hold-exclusive" => {
            hold_exclusive(endpoint_hash, id)
        }
        [command, input] if command == "analyze" => {
            println!(
                "{}",
                serde_json::to_string(&analyze_file(Path::new(input))?)?
            );
            Ok(())
        }
        _ => Err(ProbeError::Usage),
    }
}

#[cfg(windows)]
fn capture(arguments: CaptureArgs<'_>) -> Result<(), ProbeError> {
    let endpoint_id = std::env::var("JASTREAMER_QA_ENDPOINT_ID").map_err(|_| ProbeError::Usage)?;
    let ready_id = arguments.id;
    let capture = jastreamer_wasapi_loopback_probe::wasapi::capture(
        jastreamer_wasapi_loopback_probe::wasapi::CaptureRequest {
            endpoint_id: &endpoint_id,
            expected_identity_sha256: arguments.endpoint_hash,
            target_frames: arguments.frame_count,
            on_started: &|| println!("{{\"event\":\"capture.ready:{ready_id}\"}}"),
        },
    )?;
    let bytes: Vec<u8> = capture
        .samples
        .iter()
        .flat_map(|sample| sample.to_le_bytes())
        .collect();
    std::fs::write(arguments.output, bytes)?;
    println!(
        "{}",
        serde_json::to_string(&serde_json::json!({
            "event": format!("capture.complete:{}", arguments.id),
            "analysis": analyze_file(arguments.output)?,
        }))?
    );
    Ok(())
}

#[cfg(not(windows))]
fn capture(arguments: CaptureArgs<'_>) -> Result<(), ProbeError> {
    let CaptureArgs {
        output,
        frame_count,
        endpoint_hash,
        id,
    } = arguments;
    let _ = (output, frame_count, endpoint_hash, id);
    Err(ProbeError::Usage)
}

#[cfg(windows)]
fn hold_exclusive(endpoint_hash: &str, id: &str) -> Result<(), ProbeError> {
    use std::io::Read as _;
    let endpoint_id = std::env::var("JASTREAMER_QA_ENDPOINT_ID").map_err(|_| ProbeError::Usage)?;
    jastreamer_wasapi_loopback_probe::exclusive::hold(
        jastreamer_wasapi_loopback_probe::exclusive::HoldRequest {
            endpoint_id: &endpoint_id,
            expected_identity_sha256: endpoint_hash,
            on_started: &|| println!("{{\"event\":\"exclusive.ready:{id}\"}}"),
            await_release: &|| {
                let _ = std::io::stdin().read(&mut [0_u8; 1]);
            },
        },
    )?;
    Ok(())
}

#[cfg(not(windows))]
fn hold_exclusive(_endpoint_hash: &str, _id: &str) -> Result<(), ProbeError> {
    Err(ProbeError::Usage)
}

fn analyze_file(input: &Path) -> Result<AnalysisReceipt, ProbeError> {
    let bytes = std::fs::read(input)?;
    if !bytes.len().is_multiple_of(size_of::<f32>()) {
        return Err(ProbeError::InvalidBuffer);
    }
    let samples: Vec<f32> = bytes
        .chunks_exact(4)
        .map(|chunk| f32::from_le_bytes([chunk[0], chunk[1], chunk[2], chunk[3]]))
        .collect();
    let metrics = analyze(&samples, 48_000, 2).ok_or(ProbeError::InvalidBuffer)?;
    let mono: Vec<f32> = samples
        .chunks_exact(2)
        .map(|frame| f32::midpoint(frame[0], frame[1]))
        .collect();
    let comparison = FrequencyComparison {
        sample_rate_hz: 48_000,
        dominant_hz: 1_000.0,
        rejected_hz: 440.0,
    };
    Ok(AnalysisReceipt {
        capture_sha256: hex::encode(Sha256::digest(&bytes)),
        encoding: "normalized_f32le",
        sample_rate_hz: 48_000,
        channels: 2,
        metrics,
        rejection_db: frequency_rejection_db(&mono, comparison),
        dominance_latency_ms: dominance_latency_ms(&mono, comparison),
    })
}
