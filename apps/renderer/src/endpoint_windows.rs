use crate::audio::AudioError;
use crate::endpoint::EndpointEvent;
use std::sync::mpsc::{Receiver, SyncSender, sync_channel};
use std::thread::JoinHandle;
use windows::Win32::Media::Audio::{
    DEVICE_STATE, EDataFlow, ERole, IMMDeviceEnumerator, IMMNotificationClient,
    IMMNotificationClient_Impl, MMDeviceEnumerator, eRender,
};
use windows::Win32::System::Com::{
    CLSCTX_ALL, COINIT_MULTITHREADED, CoCreateInstance, CoInitializeEx, CoUninitialize,
};
use windows::Win32::UI::Shell::PropertiesSystem::PROPERTYKEY;
use windows::core::{PCWSTR, implement};

#[implement(IMMNotificationClient)]
struct NotificationClient {
    events: SyncSender<EndpointEvent>,
}

impl IMMNotificationClient_Impl for NotificationClient {
    fn OnDeviceStateChanged(&self, _: &PCWSTR, _: DEVICE_STATE) -> windows::core::Result<()> {
        let _ = self.events.try_send(EndpointEvent::TopologyChanged);
        Ok(())
    }

    fn OnDeviceAdded(&self, _: &PCWSTR) -> windows::core::Result<()> {
        let _ = self.events.try_send(EndpointEvent::TopologyChanged);
        Ok(())
    }

    fn OnDeviceRemoved(&self, _: &PCWSTR) -> windows::core::Result<()> {
        let _ = self.events.try_send(EndpointEvent::TopologyChanged);
        Ok(())
    }

    fn OnDefaultDeviceChanged(
        &self,
        flow: EDataFlow,
        _: ERole,
        _: &PCWSTR,
    ) -> windows::core::Result<()> {
        if flow == eRender {
            let _ = self.events.try_send(EndpointEvent::DefaultChanged);
        }
        Ok(())
    }

    fn OnPropertyValueChanged(&self, _: &PCWSTR, _: &PROPERTYKEY) -> windows::core::Result<()> {
        let _ = self.events.try_send(EndpointEvent::TopologyChanged);
        Ok(())
    }
}

pub struct EndpointNotifier {
    events: Receiver<EndpointEvent>,
    stop: SyncSender<()>,
    worker: Option<JoinHandle<()>>,
}

impl EndpointNotifier {
    pub fn start() -> Result<Self, AudioError> {
        let (events_tx, events) = sync_channel(16);
        let (stop, stop_rx) = sync_channel(1);
        let (ready_tx, ready_rx) = sync_channel(1);
        let worker = std::thread::spawn(move || {
            let result = notification_thread(events_tx, stop_rx, ready_tx);
            if result.is_err() {
                return;
            }
        });
        match ready_rx.recv() {
            Ok(Ok(())) => Ok(Self {
                events,
                stop,
                worker: Some(worker),
            }),
            Ok(Err(message)) => {
                let _ = worker.join();
                Err(AudioError::Failed(message))
            }
            Err(_) => {
                let _ = worker.join();
                Err(AudioError::Failed(
                    "WASAPI notification worker failed to initialize".to_owned(),
                ))
            }
        }
    }

    pub fn try_recv(&self) -> Option<EndpointEvent> {
        self.events.try_recv().ok()
    }
}

impl Drop for EndpointNotifier {
    fn drop(&mut self) {
        let _ = self.stop.send(());
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
    }
}

fn notification_thread(
    events: SyncSender<EndpointEvent>,
    stop: Receiver<()>,
    ready: SyncSender<Result<(), String>>,
) -> Result<(), ()> {
    // SAFETY: Category 8 (FFI). This dedicated thread owns a balanced COM apartment lifetime;
    // no COM interface escapes the thread and CoUninitialize runs before thread exit.
    let initialized = unsafe { CoInitializeEx(None, COINIT_MULTITHREADED) };
    if initialized.is_err() {
        let _ = ready.send(Err(windows::core::Error::from(initialized).to_string()));
        return Err(());
    }
    // SAFETY: Category 8 (FFI). COM is initialized above and the requested CLSID/IID pair is the
    // Windows MMDeviceEnumerator contract; the typed return validates the interface pointer.
    let enumerator = unsafe {
        CoCreateInstance::<_, IMMDeviceEnumerator>(&MMDeviceEnumerator, None, CLSCTX_ALL)
    };
    let enumerator = match enumerator {
        Ok(value) => value,
        Err(error) => {
            let _ = ready.send(Err(error.to_string()));
            // SAFETY: Category 8 (FFI). This balances the successful CoInitializeEx on this thread.
            unsafe { CoUninitialize() };
            return Err(());
        }
    };
    let client: IMMNotificationClient = NotificationClient { events }.into();
    // SAFETY: Category 8 (FFI). `client` remains alive and registered on this thread until the
    // matching unregister call below; Windows owns only a COM reference during registration.
    if let Err(error) = unsafe { enumerator.RegisterEndpointNotificationCallback(&client) } {
        let _ = ready.send(Err(error.to_string()));
        // SAFETY: Category 8 (FFI). This balances the successful CoInitializeEx on this thread.
        unsafe { CoUninitialize() };
        return Err(());
    }
    let _ = ready.send(Ok(()));
    let _ = stop.recv();
    // SAFETY: Category 8 (FFI). The exact client registered above is still alive and unregister is
    // called before either COM interface is dropped.
    let _ = unsafe { enumerator.UnregisterEndpointNotificationCallback(&client) };
    drop(client);
    drop(enumerator);
    // SAFETY: Category 8 (FFI). This balances the successful CoInitializeEx on this thread.
    unsafe { CoUninitialize() };
    Ok(())
}
