use super::*;

pub(super) struct ComApartment;
impl ComApartment {
    pub(super) fn initialize() -> Result<Self, CaptureError> {
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

pub(super) struct Event(pub(super) HANDLE);
impl Event {
    pub(super) fn create() -> Result<Self, CaptureError> {
        // SAFETY: Category 8 (FFI). Null security/name parameters request a process-local auto-reset event.
        Ok(Self(unsafe {
            CreateEventW(None, false, false, PCWSTR::null())?
        }))
    }
}
impl Drop for Event {
    fn drop(&mut self) {
        // SAFETY: Category 8 (FFI). The handle was returned by CreateEventW and is closed exactly once.
        let _ = unsafe { CloseHandle(self.0) };
    }
}

pub(super) struct RunningClient(pub(super) IAudioClient);
impl Drop for RunningClient {
    fn drop(&mut self) {
        // SAFETY: Category 8 (FFI). The typed COM client remains valid until this guard is dropped.
        let _ = unsafe { self.0.Stop() };
    }
}

pub(super) struct MixFormat(*mut WAVEFORMATEX);
impl MixFormat {
    pub(super) fn get(client: &IAudioClient) -> Result<Self, CaptureError> {
        // SAFETY: Category 8 (FFI). The initialized typed audio client returns COM-task allocated format memory.
        Ok(Self(unsafe { client.GetMixFormat()? }))
    }
    pub(super) fn value(&self) -> &WAVEFORMATEX {
        // SAFETY: Categories 5/8 (FFI). GetMixFormat returned a non-null initialized WAVEFORMATEX
        // whose allocation remains owned by this wrapper until Drop.
        unsafe { &*self.0 }
    }
}
impl Drop for MixFormat {
    fn drop(&mut self) {
        // SAFETY: Category 8 (FFI). This pointer came from GetMixFormat and is freed exactly once.
        unsafe { CoTaskMemFree(Some(self.0.cast::<c_void>())) };
    }
}
