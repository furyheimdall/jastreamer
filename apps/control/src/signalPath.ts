import type { NativeAndroidAudioState } from "./NativeAndroidOutput";
import type { NativeWindowsState } from "./NativeWindowsOutput";

// The player bar badge summarises the one thing both native outputs agree on: whether the
// active path bypasses the platform mixer and whether that path stayed bit-transparent.
export type SignalPathBadge = {
  tone: "qualified" | "muted";
  reasonKey: string | null;
  detail: { rate: number; validBits: number; containerBits: number; device: string };
} | null;

// Normalised input so Windows exclusive mode and Android direct USB share one rule set.
export type SignalPathInput = {
  // The active engine bypasses the platform mixer (WASAPI exclusive / direct USB).
  direct: boolean;
  stopped: boolean;
  bitTransparent: boolean;
  reason: string;
  rate: number;
  validBits: number;
  containerBits: number;
  device: string;
};

export function signalPathBadge(input: SignalPathInput | null): SignalPathBadge {
  if (!input || !input.direct || input.stopped) return null;
  const detail = {
    rate: input.rate,
    validBits: input.validBits,
    containerBits: input.containerBits,
    device: input.device,
  };
  return input.bitTransparent
    ? { tone: "qualified", reasonKey: null, detail }
    : { tone: "muted", reasonKey: input.reason || null, detail };
}

export function windowsSignalPath(audio: NativeWindowsState["audio"] | null | undefined): SignalPathBadge {
  const actual = audio?.actual;
  if (!audio || !audio.enabled || !actual) return null;
  return signalPathBadge({
    direct: actual.mode === "exclusive",
    stopped: audio.state === "stopped",
    bitTransparent: actual.bit_transparent,
    reason: actual.reason,
    rate: actual.sample_rate,
    validBits: actual.valid_bits,
    containerBits: actual.container_bits,
    device: actual.name,
  });
}

export function androidSignalPath(audio: NativeAndroidAudioState | null | undefined): SignalPathBadge {
  const actual = audio?.actual;
  if (!audio || !actual) return null;
  return signalPathBadge({
    direct: actual.engine === "usb_direct",
    stopped: audio.state === "stopped",
    bitTransparent: actual.bit_transparent,
    reason: actual.reason,
    rate: actual.sample_rate,
    validBits: actual.valid_bits,
    containerBits: actual.container_bits,
    device: actual.device_name,
  });
}
