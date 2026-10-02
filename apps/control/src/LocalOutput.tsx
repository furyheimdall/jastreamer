import { forwardRef, useImperativeHandle, useRef } from "react";
import BrowserOutput, { type BrowserOutputHandle, type BrowserOutputProps } from "./BrowserOutput";
import NativeAndroidOutput, {
  nativeAndroidAudioBridge,
  type NativeAndroidAudioState,
  type NativeAndroidOutputHandle,
  type NativeAndroidUnsupportedFormat,
} from "./NativeAndroidOutput";
import NativeDesktopOutput, {
  requestNativeDesktopAudio,
  type JastreamerDesktopAudioBridge,
  type NativeDesktopConfiguration,
  type NativeDesktopOutputHandle,
  type NativeDesktopState,
} from "./NativeDesktopOutput";
import type { Device } from "./types";

export interface LocalOutputHandle {
  connect: () => Promise<Device>;
  connectAutomatically: () => Promise<Device | null>;
  disconnect: () => Promise<void>;
  setVolume: (volume: number) => Promise<void>;
  rename: (name: string) => Promise<void>;
  retryPlayback?: () => void;
  configureDesktop: (configuration: NativeDesktopConfiguration) => Promise<NativeDesktopState>;
  configureAndroid: (usbDirect: boolean, unsupportedFormat: NativeAndroidUnsupportedFormat) => Promise<void>;
}

export interface LocalOutputProps extends BrowserOutputProps {
  bridgeError: string;
  onRecoveryChange: (recovering: boolean, message: string) => void;
  desktopBridge: JastreamerDesktopAudioBridge | null;
  desktopState: NativeDesktopState | null;
  onDesktopStateChange: (state: NativeDesktopState) => void;
  onAndroidAudioChange: (audio: NativeAndroidAudioState | null) => void;
}

export function hasNativeAndroidAudio(): boolean {
  return typeof window !== "undefined" && "JastreamerAndroidAudio" in window;
}

const LocalOutput = forwardRef<LocalOutputHandle, LocalOutputProps>(function LocalOutput(
  {
    bridgeError,
    onRecoveryChange,
    desktopBridge,
    desktopState,
    onDesktopStateChange,
    onAndroidAudioChange,
    ...browserProps
  },
  ref,
) {
  const nativeAndroidPresent = hasNativeAndroidAudio();
  const androidBridge = nativeAndroidAudioBridge();
  const nativeDesktopEnabled = Boolean(desktopBridge && desktopState?.audio.enabled);
  const browserRef = useRef<BrowserOutputHandle>(null);
  const androidRef = useRef<NativeAndroidOutputHandle>(null);
  const desktopRef = useRef<NativeDesktopOutputHandle>(null);

  useImperativeHandle(ref, () => ({
    connect() {
      const output = nativeAndroidPresent
        ? androidRef.current
        : nativeDesktopEnabled
          ? desktopRef.current
          : browserRef.current;
      return output
        ? output.connect()
        : Promise.reject(new Error(browserProps.registrationError));
    },
    connectAutomatically() {
      if (nativeAndroidPresent) return androidRef.current?.connectAutomatically() ?? Promise.resolve(null);
      if (nativeDesktopEnabled) return desktopRef.current?.connectAutomatically() ?? Promise.resolve(null);
      return browserRef.current?.connect() ?? Promise.resolve(null);
    },
    async disconnect() {
      const output = nativeAndroidPresent
        ? androidRef.current
        : nativeDesktopEnabled
          ? desktopRef.current
          : browserRef.current;
      await output?.disconnect();
    },
    async setVolume(volume) {
      const output = nativeAndroidPresent
        ? androidRef.current
        : nativeDesktopEnabled
          ? desktopRef.current
          : browserRef.current;
      if (!output) throw new Error(browserProps.actionError);
      await output.setVolume(volume);
    },
    rename(name) {
      const output = nativeAndroidPresent
        ? androidRef.current
        : nativeDesktopEnabled
          ? desktopRef.current
          : browserRef.current;
      return output
        ? output.rename(name)
        : Promise.reject(new Error(browserProps.registrationError));
    },
    async configureDesktop(configuration) {
      if (!desktopBridge) throw new Error(bridgeError);
      if (!nativeDesktopEnabled) {
        await browserRef.current?.disconnect({ onlyWhenStopped: true });
      }
      const next = await requestNativeDesktopAudio(desktopBridge, "configure", configuration);
      onDesktopStateChange(next);
      return next;
    },
    async configureAndroid(usbDirect, unsupportedFormat) {
      const output = androidRef.current;
      if (!output) throw new Error(bridgeError);
      await output.configure(usbDirect, unsupportedFormat);
    },
    ...(!nativeAndroidPresent && !nativeDesktopEnabled
      ? { retryPlayback: () => browserRef.current?.retryPlayback() }
      : {}),
  }), [
    bridgeError,
    browserProps.actionError,
    browserProps.registrationError,
    nativeAndroidPresent,
    nativeDesktopEnabled,
    onDesktopStateChange,
    desktopBridge,
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

  if (desktopBridge && !desktopState) return null;

  if (desktopBridge && desktopState?.audio.enabled) {
    return (
      <NativeDesktopOutput
        ref={desktopRef}
        bridge={desktopBridge}
        state={desktopState}
        name={browserProps.name}
        registrationError={browserProps.registrationError}
        bridgeError={bridgeError}
        onStateChange={onDesktopStateChange}
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
