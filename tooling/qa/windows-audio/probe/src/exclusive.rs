use crate::wasapi::CaptureError;
use sha2::{Digest, Sha256};
use std::ffi::c_void;
use windows::Win32::Foundation::{CloseHandle, HANDLE};
use windows::Win32::Media::Audio::{
    AUDCLNT_SHAREMODE_EXCLUSIVE, AUDCLNT_STREAMFLAGS_EVENTCALLBACK, IAudioClient,
    IMMDeviceEnumerator, IMMEndpoint, MMDeviceEnumerator, WAVEFORMATEX, eRender,
};
use windows::Win32::System::Com::{
    CLSCTX_ALL, COINIT_MULTITHREADED, CoCreateInstance, CoInitializeEx, CoTaskMemFree,
    CoUninitialize,
};
use windows::Win32::System::Threading::CreateEventW;
use windows::core::{HSTRING, Interface, PCWSTR};

pub struct HoldRequest<'a> {
    pub endpoint_id: &'a str,
    pub expected_identity_sha256: &'a str,
    pub on_started: &'a dyn Fn(),
    pub await_release: &'a dyn Fn(),
}

struct ComApartment;
impl ComApartment {
    fn initialize() -> Result<Self, CaptureError> {
        // SAFETY: Category 8 (FFI). This thread owns the balanced COM apartment lifetime.
        unsafe { CoInitializeEx(None, COINIT_MULTITHREADED).ok()? };
        Ok(Self)
    }
}
impl Drop for ComApartment {
    fn drop(&mut self) {
        // SAFETY: Category 8 (FFI). This balances this thread's successful CoInitializeEx.
        unsafe { CoUninitialize() };
    }
}

struct Event(HANDLE);
impl Event {
    fn create() -> Result<Self, CaptureError> {
        // SAFETY: Category 8 (FFI). Null security/name parameters request a process-local event.
        Ok(Self(unsafe {
            CreateEventW(None, false, false, PCWSTR::null())?
        }))
    }
}
impl Drop for Event {
    fn drop(&mut self) {
        // SAFETY: Category 8 (FFI). The CreateEventW handle is closed exactly once.
        let _ = unsafe { CloseHandle(self.0) };
    }
}

struct RunningClient(IAudioClient);
impl Drop for RunningClient {
    fn drop(&mut self) {
        // SAFETY: Category 8 (FFI). The typed client remains valid until this guard drops.
        let _ = unsafe { self.0.Stop() };
    }
}

struct MixFormat(*mut WAVEFORMATEX);
impl MixFormat {
    fn get(client: &IAudioClient) -> Result<Self, CaptureError> {
        // SAFETY: Category 8 (FFI). The typed client returns COM-task allocated format memory.
        Ok(Self(unsafe { client.GetMixFormat()? }))
    }
}
impl Drop for MixFormat {
    fn drop(&mut self) {
        // SAFETY: Category 8 (FFI). This GetMixFormat pointer is freed exactly once.
        unsafe { CoTaskMemFree(Some(self.0.cast::<c_void>())) };
    }
}

pub fn hold(request: HoldRequest<'_>) -> Result<(), CaptureError> {
    if hex::encode(Sha256::digest(request.endpoint_id.as_bytes()))
        != request.expected_identity_sha256
    {
        return Err(CaptureError::EndpointChanged);
    }
    let _apartment = ComApartment::initialize()?;
    // SAFETY: Category 8 (FFI). COM is initialized and the CLSID/IID request a typed enumerator.
    let enumerator = unsafe {
        CoCreateInstance::<_, IMMDeviceEnumerator>(&MMDeviceEnumerator, None, CLSCTX_ALL)?
    };
    let endpoint = HSTRING::from(request.endpoint_id);
    // SAFETY: Category 8 (FFI). HSTRING supplies a terminated endpoint ID for the call.
    let device = unsafe { enumerator.GetDevice(&endpoint)? };
    let endpoint_interface: IMMEndpoint = device.cast()?;
    // SAFETY: Category 8 (FFI). The typed endpoint writes a valid flow discriminator.
    if unsafe { endpoint_interface.GetDataFlow()? } != eRender {
        return Err(CaptureError::EndpointNotRender);
    }
    // SAFETY: Category 8 (FFI). The render endpoint activates its documented audio client.
    let client: IAudioClient = unsafe { device.Activate(CLSCTX_ALL, None)? };
    let format = MixFormat::get(&client)?;
    let mut period = 0_i64;
    // SAFETY: Category 8 (FFI). The typed client writes its minimum device period.
    unsafe { client.GetDevicePeriod(None, Some(&mut period))? };
    let event = Event::create()?;
    // SAFETY: Category 8 (FFI). Mix format and equal exclusive duration/period remain valid.
    unsafe {
        client.Initialize(
            AUDCLNT_SHAREMODE_EXCLUSIVE,
            AUDCLNT_STREAMFLAGS_EVENTCALLBACK,
            period,
            period,
            format.0,
            None,
        )?
    };
    // SAFETY: Category 8 (FFI). The event remains valid for the client lifetime.
    unsafe { client.SetEventHandle(event.0)? };
    // SAFETY: Category 8 (FFI). The initialized client is stopped by RunningClient.
    unsafe { client.Start()? };
    let running = RunningClient(client);
    (request.on_started)();
    (request.await_release)();
    drop(running);
    Ok(())
}
