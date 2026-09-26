import { forwardRef, useImperativeHandle, useRef } from "react";
import BrowserOutput, { type BrowserOutputHandle, type BrowserOutputProps } from "./BrowserOutput";
import NativeAndroidOutput, {
  nativeAndroidAudioBridge,
  type NativeAndroidAudioState,
  type NativeAndroidOutputHandle,
  type NativeAndroidUnsupportedFormat,
} from "./NativeAndroidOutput";
import NativeWindowsOutput, {
  requestNativeWindowsAudio,
  type JastreamerWindowsAudioBridge,
  type NativeWindowsConfiguration,
  type NativeWindowsOutputHandle,
  type NativeWindowsState,
} from "./NativeWindowsOutput";
import type { Device } from "./types";

export interface LocalOutputHandle {
  connect: () => Promise<Device>;
  connectAutomatically: () => Promise<Device | null>;
  disconnect: () => Promise<void>;
  setVolume: (volume: number) => Promise<void>;
  rename: (name: string) => Promise<void>;
  retryPlayback?: () => void;
  configureWindows: (configuration: NativeWindowsConfiguration) => Promise<NativeWindowsState>;
  configureAndroid: (usbDirect: boolean, unsupportedFormat: NativeAndroidUnsupportedFormat) => Promise<void>;
}

export interface LocalOutputProps extends BrowserOutputProps {
  bridgeError: string;
  onRecoveryChange: (recovering: boolean, message: string) => void;
  windowsBridge: JastreamerWindowsAudioBridge | null;
  windowsState: NativeWindowsState | null;
  onWindowsStateChange: (state: NativeWindowsState) => void;
  onAndroidAudioChange: (audio: NativeAndroidAudioState | null) => void;
}

export function hasNativeAndroidAudio(): boolean {
  return typeof window !== "undefined" && "JastreamerAndroidAudio" in window;
}

const LocalOutput = forwardRef<LocalOutputHandle, LocalOutputProps>(function LocalOutput(
  {
    bridgeError,
    onRecoveryChange,
    windowsBridge,
    windowsState,
    onWindowsStateChange,
    onAndroidAudioChange,
    ...browserProps
  },
  ref,
) {
  const nativeAndroidPresent = hasNativeAndroidAudio();
  const androidBridge = nativeAndroidAudioBridge();
  const nativeWindowsEnabled = Boolean(windowsBridge && windowsState?.audio.enabled);
  const browserRef = useRef<BrowserOutputHandle>(null);
  const androidRef = useRef<NativeAndroidOutputHandle>(null);
  const windowsRef = useRef<NativeWindowsOutputHandle>(null);

  useImperativeHandle(ref, () => ({
    connect() {
      const output = nativeAndroidPresent
        ? androidRef.current
        : nativeWindowsEnabled
          ? windowsRef.current
          : browserRef.current;
      return output
        ? output.connect()
        : Promise.reject(new Error(browserProps.registrationError));
    },
    connectAutomatically() {
      if (nativeAndroidPresent) return androidRef.current?.connectAutomatically() ?? Promise.resolve(null);
      if (nativeWindowsEnabled) return windowsRef.current?.connectAutomatically() ?? Promise.resolve(null);
      return browserRef.current?.connect() ?? Promise.resolve(null);
    },
    async disconnect() {
      const output = nativeAndroidPresent
        ? androidRef.current
        : nativeWindowsEnabled
          ? windowsRef.current
          : browserRef.current;
      await output?.disconnect();
    },
    async setVolume(volume) {
      const output = nativeAndroidPresent
        ? androidRef.current
        : nativeWindowsEnabled
          ? windowsRef.current
          : browserRef.current;
      if (!output) throw new Error(browserProps.actionError);
      await output.setVolume(volume);
    },
    rename(name) {
      const output = nativeAndroidPresent
        ? androidRef.current
        : nativeWindowsEnabled
          ? windowsRef.current
          : browserRef.current;
      return output
        ? output.rename(name)
        : Promise.reject(new Error(browserProps.registrationError));
    },
    async configureWindows(configuration) {
      if (!windowsBridge) throw new Error(bridgeError);
      if (!nativeWindowsEnabled) {
        await browserRef.current?.disconnect({ onlyWhenStopped: true });
      }
      const next = await requestNativeWindowsAudio(windowsBridge, "configure", configuration);
      onWindowsStateChange(next);
      return next;
    },
    async configureAndroid(usbDirect, unsupportedFormat) {
      const output = androidRef.current;
      if (!output) throw new Error(bridgeError);
      await output.configure(usbDirect, unsupportedFormat);
    },
    ...(!nativeAndroidPresent && !nativeWindowsEnabled
      ? { retryPlayback: () => browserRef.current?.retryPlayback() }
      : {}),
  }), [
    bridgeError,
    browserProps.actionError,
    browserProps.registrationError,
    nativeAndroidPresent,
    nativeWindowsEnabled,
    onWindowsStateChange,
    windowsBridge,
  ]);

  if (nativeAndroidPresent) {
    return (
      <NativeAndroidOutput
        ref={androidRef}
        bridge={androidBridge}
        name={browserProps.name}
        registrationError={browserProps.registrationError}
        bridgeError={bridgeError}
        onDeviceChange={browserProps.onDeviceChange}
        onRecoveryChange={onRecoveryChange}
        onError={browserProps.onError}
        onVolumeChange={browserProps.onVolumeChange}
        onAudioStateChange={onAndroidAudioChange}
      />
    );
  }

  if (windowsBridge && !windowsState) return null;

  if (windowsBridge && windowsState?.audio.enabled) {
    return (
      <NativeWindowsOutput
        ref={windowsRef}
        bridge={windowsBridge}
        state={windowsState}
        name={browserProps.name}
        registrationError={browserProps.registrationError}
        bridgeError={bridgeError}
        onStateChange={onWindowsStateChange}
        onDeviceChange={browserProps.onDeviceChange}
        onRecoveryChange={onRecoveryChange}
        onError={browserProps.onError}
        onVolumeChange={browserProps.onVolumeChange}
      />
    );
  }

  return <BrowserOutput ref={browserRef} {...browserProps} />;
});

export default LocalOutput;
