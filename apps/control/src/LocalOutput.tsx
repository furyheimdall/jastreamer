import { forwardRef, useImperativeHandle, useRef } from "react";
import BrowserOutput, { type BrowserOutputHandle, type BrowserOutputProps } from "./BrowserOutput";
import NativeAndroidOutput, {
  nativeAndroidAudioBridge,
  type NativeAndroidOutputHandle,
} from "./NativeAndroidOutput";
import type { Device } from "./types";

export interface LocalOutputHandle {
  connect: () => Promise<Device>;
  rename: (name: string) => Promise<void>;
  retryPlayback?: () => void;
}

export interface LocalOutputProps extends BrowserOutputProps {
  bridgeError: string;
  onRecoveryChange: (recovering: boolean, message: string) => void;
}

export function hasNativeAndroidAudio(): boolean {
  return typeof window !== "undefined" && "JastreamerAndroidAudio" in window;
}

const LocalOutput = forwardRef<LocalOutputHandle, LocalOutputProps>(function LocalOutput(
  { bridgeError, onRecoveryChange, ...browserProps },
  ref,
) {
  const nativePresent = hasNativeAndroidAudio();
  const bridge = nativeAndroidAudioBridge();
  const browserRef = useRef<BrowserOutputHandle>(null);
  const nativeRef = useRef<NativeAndroidOutputHandle>(null);

  useImperativeHandle(ref, () => ({
    connect() {
      const output = nativePresent ? nativeRef.current : browserRef.current;
      return output
        ? output.connect()
        : Promise.reject(new Error(browserProps.registrationError));
    },
    rename(name) {
      const output = nativePresent ? nativeRef.current : browserRef.current;
      return output
        ? output.rename(name)
        : Promise.reject(new Error(browserProps.registrationError));
    },
    ...(nativePresent ? {} : { retryPlayback: () => browserRef.current?.retryPlayback() }),
  }), [nativePresent, browserProps.registrationError]);

  if (nativePresent) {
    return (
      <NativeAndroidOutput
        ref={nativeRef}
        bridge={bridge}
        name={browserProps.name}
        registrationError={browserProps.registrationError}
        bridgeError={bridgeError}
        onDeviceChange={browserProps.onDeviceChange}
        onRecoveryChange={onRecoveryChange}
        onError={browserProps.onError}
      />
    );
  }

  return <BrowserOutput ref={browserRef} {...browserProps} />;
});

export default LocalOutput;
