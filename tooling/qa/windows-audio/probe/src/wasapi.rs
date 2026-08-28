use sha2::{Digest, Sha256};
use std::ffi::c_void;
use std::mem::size_of;
use std::slice;
use windows::Win32::Foundation::{CloseHandle, HANDLE, WAIT_OBJECT_0};
use windows::Win32::Media::Audio::{
    AUDCLNT_BUFFERFLAGS_SILENT, AUDCLNT_SHAREMODE_SHARED, AUDCLNT_STREAMFLAGS_EVENTCALLBACK,
    AUDCLNT_STREAMFLAGS_LOOPBACK, IAudioCaptureClient, IAudioClient, IMMDeviceEnumerator,
    IMMEndpoint, MMDeviceEnumerator, WAVEFORMATEX, WAVEFORMATEXTENSIBLE, eRender,
};
use windows::Win32::Media::Multimedia::{KSDATAFORMAT_SUBTYPE_IEEE_FLOAT, WAVE_FORMAT_IEEE_FLOAT};
use windows::Win32::System::Com::{
    CLSCTX_ALL, COINIT_MULTITHREADED, CoCreateInstance, CoInitializeEx, CoTaskMemFree,
    CoUninitialize,
};
use windows::Win32::System::Threading::{CreateEventW, WaitForSingleObject};
use windows::core::{HSTRING, Interface, PCWSTR};

const EVENT_TIMEOUT_MS: u32 = 5_000;
const WAVE_FORMAT_EXTENSIBLE: u16 = 0xfffe;

mod resources;
use resources::{ComApartment, Event, MixFormat, RunningClient};

#[derive(Debug, thiserror::Error)]
pub enum CaptureError {
    #[error("WASAPI_ERROR: {0}")]
    Windows(#[from] windows::core::Error),
    #[error("ENDPOINT_IDENTITY_CHANGED")]
    EndpointChanged,
    #[error("ENDPOINT_NOT_RENDER")]
    EndpointNotRender,
    #[error("CAPTURE_FORMAT_FAILED")]
    UnsupportedFormat,
    #[error("CAPTURE_EVENT_TIMEOUT")]
    Timeout,
    #[error("CAPTURE_BUFFER_INVALID")]
    InvalidBuffer,
}

pub struct Capture {
    pub samples: Vec<f32>,
    pub sample_rate_hz: u32,
    pub channels: u16,
}

pub struct CaptureRequest<'a> {
    pub endpoint_id: &'a str,
    pub expected_identity_sha256: &'a str,
    pub target_frames: usize,
    pub on_started: &'a dyn Fn(),
}

pub fn capture(request: CaptureRequest<'_>) -> Result<Capture, CaptureError> {
    if hex::encode(Sha256::digest(request.endpoint_id.as_bytes()))
        != request.expected_identity_sha256
    {
        return Err(CaptureError::EndpointChanged);
    }
    let _apartment = ComApartment::initialize()?;
    // SAFETY: Category 8 (FFI). COM is initialized and the CLSID/IID pair requests a typed MMDeviceEnumerator.
    let enumerator = unsafe {
        CoCreateInstance::<_, IMMDeviceEnumerator>(&MMDeviceEnumerator, None, CLSCTX_ALL)?
    };
    let endpoint = HSTRING::from(request.endpoint_id);
    // SAFETY: Category 8 (FFI). The HSTRING supplies a terminated endpoint ID for the typed enumerator.
    let device = unsafe { enumerator.GetDevice(&endpoint)? };
    let endpoint_interface: IMMEndpoint = device.cast()?;
    // SAFETY: Category 8 (FFI). The typed endpoint interface writes a valid EDataFlow discriminator.
    if unsafe { endpoint_interface.GetDataFlow()? } != eRender {
        return Err(CaptureError::EndpointNotRender);
    }
    // SAFETY: Category 8 (FFI). The typed device returns a COM-task allocated terminated endpoint ID.
    let observed_id = unsafe { device.GetId()? };
    // SAFETY: Category 8 (FFI). GetId guarantees a valid terminated UTF-16 string until CoTaskMemFree.
    let observed = unsafe { observed_id.to_string() }.map_err(|_| CaptureError::EndpointChanged)?;
    // SAFETY: Category 8 (FFI). This pointer came from IMMDevice::GetId and is freed exactly once here.
    unsafe { CoTaskMemFree(Some(observed_id.0.cast::<c_void>())) };
    if hex::encode(Sha256::digest(observed.as_bytes())) != request.expected_identity_sha256 {
        return Err(CaptureError::EndpointChanged);
    }
    // SAFETY: Category 8 (FFI). The typed render endpoint activates its documented IAudioClient service.
    let client: IAudioClient = unsafe { device.Activate(CLSCTX_ALL, None)? };
    let format = MixFormat::get(&client)?;
    validate_format(format.value())?;
    let event = Event::create()?;
    // SAFETY: Category 8 (FFI). Format memory remains alive, shared loopback flags are valid for a render endpoint,
    // and the event handle remains alive through the entire initialized stream lifetime.
    unsafe {
        client.Initialize(
            AUDCLNT_SHAREMODE_SHARED,
            AUDCLNT_STREAMFLAGS_LOOPBACK | AUDCLNT_STREAMFLAGS_EVENTCALLBACK,
            0,
            0,
            format.0,
            None,
        )?
    };
    // SAFETY: Category 8 (FFI). The event handle is valid and owned until capture completes.
    unsafe { client.SetEventHandle(event.0)? };
    // SAFETY: Category 8 (FFI). Initialize succeeded and returns the typed capture service for this stream.
    let capture_client: IAudioCaptureClient = unsafe { client.GetService()? };
    // SAFETY: Category 8 (FFI). The initialized client may be started once before the balanced Stop guard.
    unsafe { client.Start()? };
    (request.on_started)();
    let running = RunningClient(client);
    let result = collect(
        &capture_client,
        CapturePlan {
            event: &event,
            format: format.value(),
            target_frames: request.target_frames,
        },
    );
    drop(running);
    result
}

fn validate_format(format: &WAVEFORMATEX) -> Result<(), CaptureError> {
    if format.nSamplesPerSec != 48_000 || format.nChannels != 2 || format.wBitsPerSample != 32 {
        return Err(CaptureError::UnsupportedFormat);
    }
    if format.wFormatTag == WAVE_FORMAT_IEEE_FLOAT as u16 {
        return Ok(());
    }
    if format.wFormatTag != WAVE_FORMAT_EXTENSIBLE
        || usize::from(format.cbSize) + size_of::<WAVEFORMATEX>()
            < size_of::<WAVEFORMATEXTENSIBLE>()
    {
        return Err(CaptureError::UnsupportedFormat);
    }
    let extended = format as *const WAVEFORMATEX as *const WAVEFORMATEXTENSIBLE;
    // SAFETY: Categories 6/8 (FFI). cbSize above proves the allocation includes WAVEFORMATEXTENSIBLE;
    // WAVEFORMATEX is its repr(C) prefix and GetMixFormat provides proper alignment.
    if unsafe { (*extended).SubFormat } != KSDATAFORMAT_SUBTYPE_IEEE_FLOAT {
        return Err(CaptureError::UnsupportedFormat);
    }
    Ok(())
}

struct CapturePlan<'a> {
    event: &'a Event,
    format: &'a WAVEFORMATEX,
    target_frames: usize,
}

struct Packet {
    data: *mut u8,
    frames: u32,
    flags: u32,
}

fn collect(client: &IAudioCaptureClient, plan: CapturePlan<'_>) -> Result<Capture, CaptureError> {
    let channels = usize::from(plan.format.nChannels);
    let mut samples = Vec::with_capacity(plan.target_frames.saturating_mul(channels));
    while samples.len() / channels < plan.target_frames {
        // SAFETY: Category 8 (FFI). The event handle remains valid and this bounded wait consumes capture events.
        if unsafe { WaitForSingleObject(plan.event.0, EVENT_TIMEOUT_MS) } != WAIT_OBJECT_0 {
            return Err(CaptureError::Timeout);
        }
        loop {
            // SAFETY: Category 8 (FFI). The initialized capture service may be queried after its event signals.
            let packet_frames = unsafe { client.GetNextPacketSize()? };
            if packet_frames == 0 {
                break;
            }
            let mut data = std::ptr::null_mut();
            let mut frames = 0;
            let mut flags = 0;
            // SAFETY: Category 8 (FFI). Out pointers are valid for writes and the packet stays borrowed until ReleaseBuffer.
            unsafe { client.GetBuffer(&mut data, &mut frames, &mut flags, None, None)? };
            let converted = packet_samples(
                Packet {
                    data,
                    frames,
                    flags,
                },
                channels,
            );
            // SAFETY: Category 8 (FFI). This releases the exact frame count returned by the preceding GetBuffer.
            unsafe { client.ReleaseBuffer(frames)? };
            samples.extend_from_slice(&converted?);
        }
    }
    samples.truncate(plan.target_frames * channels);
    Ok(Capture {
        samples,
        sample_rate_hz: plan.format.nSamplesPerSec,
        channels: plan.format.nChannels,
    })
}

fn packet_samples(packet: Packet, channels: usize) -> Result<Vec<f32>, CaptureError> {
    let length = usize::try_from(packet.frames)
        .map_err(|_| CaptureError::InvalidBuffer)?
        .checked_mul(channels)
        .ok_or(CaptureError::InvalidBuffer)?;
    if packet.flags
        & u32::try_from(AUDCLNT_BUFFERFLAGS_SILENT.0).map_err(|_| CaptureError::InvalidBuffer)?
        != 0
    {
        return Ok(vec![0.0; length]);
    }
    if packet.data.is_null() {
        return Err(CaptureError::InvalidBuffer);
    }
    // SAFETY: Categories 6/8/10 (FFI). GetBuffer guarantees `frames * channels` initialized 32-bit
    // float samples for the validated mix format, aligned and valid until ReleaseBuffer.
    Ok(unsafe { slice::from_raw_parts(packet.data.cast::<f32>(), length) }.to_vec())
}
