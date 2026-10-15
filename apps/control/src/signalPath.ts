import type { NativeAndroidAudioState } from "./NativeAndroidOutput";
import type { NativeDesktopState } from "./NativeDesktopOutput";
import type { Device, PlayerState } from "./types";

// The player bar badge summarises the one thing both native outputs agree on: whether the
// active path bypasses the platform mixer and whether that path stayed bit-transparent.
export type SignalPathBadge = {
  tone: "qualified" | "muted";
  reasonKey: string | null;
  detail: { rate: number; validBits: number; containerBits: number; device: string };
} | null;

// Normalised input so Desktop exclusive mode and Android direct USB share one rule set.
export type SignalPathInput = {
  // The active engine bypasses the platform mixer (WASAPI / CoreAudio exclusive / direct USB).
  direct: boolean;
  // The engine is currently playing or paused; loaded/error/stopped engines may retain a
  // previous path and must not be labelled.
  active: boolean;
  bitTransparent: boolean;
  reason: string;
  rate: number;
  validBits: number;
  containerBits: number;
  device: string;
};

export function signalPathBadge(input: SignalPathInput | null): SignalPathBadge {
  if (!input || !input.direct || !input.active) return null;
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

export function desktopSignalPath(audio: NativeDesktopState["audio"] | null | undefined): SignalPathBadge {
  const actual = audio?.actual;
  if (!audio || !audio.enabled || !actual) return null;
  return signalPathBadge({
    direct: actual.mode === "exclusive",
    active: audio.state === "playing" || audio.state === "paused",
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
    active: (audio.state === "playing" || audio.state === "paused") && !audio.error,
    bitTransparent: actual.bit_transparent,
    reason: actual.reason,
    rate: actual.sample_rate,
    validBits: actual.valid_bits,
    containerBits: actual.container_bits,
    device: actual.device_name,
  });
}

// Server-delivery evidence, validated against the current selection. It only describes the
// server's side of the path; the receiver and its DAC are never part of the claim.
export type ServerSignalPath = {
  sampleRate: number;
  validBits: number;
  channels: number;
} & (
  // The server sent the file itself, so no PCM sample width applies.
  | { mode: "original"; containerBits: null }
  | { mode: "pcm"; containerBits: number }
);

function positiveInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value > 0;
}

// Proof may only describe playback that is current and healthy: a pending command, error,
// status warning, a failed refresh, or an output change in flight could all mean the last
// reported path no longer applies.
function settledPlayback(player: PlayerState | null, unsettled: boolean): player is PlayerState & { track: NonNullable<PlayerState["track"]> } {
  return Boolean(
    player
    && !unsettled
    && (player.state === "playing" || player.state === "paused")
    && player.track
    && !player.pending_command
    && !player.error
    && !player.status_warning,
  );
}

// Native badges describe this device's own output engine, so they only apply while this
// device is the selected output; a lingering native state must never label a network output.
export function localSignalPath(
  badge: SignalPathBadge,
  player: PlayerState | null,
  localOutputID: string | undefined,
  unsettled: boolean,
): SignalPathBadge {
  if (!badge || !localOutputID || !settledPlayback(player, unsettled) || player.renderer_id !== localOutputID) return null;
  return badge;
}

// Unknown, lossy, or transformed paths arrive without evidence (or with inconsistent
// evidence) and yield null: the badge is hidden, never turned into a negative claim.
export function serverSignalPath(
  player: PlayerState | null,
  output: Device | undefined,
  unsettled: boolean,
): ServerSignalPath | null {
  if (!settledPlayback(player, unsettled)) return null;
  if (!output || !output.online || output.protocol === "browser" || output.id !== player.renderer_id) return null;
  const path = player.signal_path;
  if (!path || path.scope !== "server") return null;
  if (path.renderer_id !== player.renderer_id || path.track_id !== player.track.id) return null;
  if (!positiveInteger(path.sample_rate) || !positiveInteger(path.valid_bits) || !positiveInteger(path.channels)) return null;
  const format = { sampleRate: path.sample_rate, validBits: path.valid_bits, channels: path.channels };
  if (path.mode === "original") {
    return path.container_bits === 0 ? { mode: "original", containerBits: null, ...format } : null;
  }
  if (path.mode === "pcm") {
    return positiveInteger(path.container_bits) && path.container_bits >= path.valid_bits
      ? { mode: "pcm", containerBits: path.container_bits, ...format }
      : null;
  }
  return null;
}
