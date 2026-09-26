import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import LocalOutput, { hasNativeAndroidAudio, type LocalOutputHandle } from "./LocalOutput";
import type { NativeAndroidAudioState, NativeAndroidUsbDirect } from "./NativeAndroidOutput";
import {
  nativeWindowsAudioBridge,
  parseNativeWindowsState,
  requestNativeWindowsAudio,
  type NativeWindowsConfiguration,
  type NativeWindowsState,
} from "./NativeWindowsOutput";
import { useI18n, type MessageKey } from "./i18n";
import { embeddedClient, isPhone, localOutputName } from "./device";
import type { Device, PairingRequest, PairingStatus, PlayerState, RepeatMode, StatusWarning } from "./types";

interface PlayerBarProps {
  revision: number;
  phoneExpanded: boolean;
  onPhoneExpandedChange: (expanded: boolean) => void;
  onNotice: (message: string, error?: boolean) => void;
  onStatusWarning: (warning: StatusWarning | null, reopen?: boolean) => void;
  onQueueChange: () => void;
  onShowQueue: () => void;
}

const WINDOWS_AUDIO_STATE_MESSAGE: Record<NativeWindowsState["audio"]["state"], MessageKey> = {
  stopped: "player.windows.state.stopped",
  loaded: "player.windows.state.loaded",
  playing: "player.windows.state.playing",
  paused: "player.windows.state.paused",
  error: "player.windows.state.error",
};

const ANDROID_AUDIO_STATE_MESSAGE: Record<NativeAndroidAudioState["state"], MessageKey> = {
  stopped: "player.android.state.stopped",
  loaded: "player.android.state.loaded",
  playing: "player.android.state.playing",
  paused: "player.android.state.paused",
  error: "player.android.state.error",
};

const USB_DIRECT_ACTIVE = new Set<NativeAndroidUsbDirect["state"]>(["opening_source", "opening", "playing"]);

const ANDROID_USB_DIRECT_STATE_MESSAGE: Record<NativeAndroidUsbDirect["state"], MessageKey> = {
  idle: "player.android.usbDirect.state.idle",
  opening_source: "player.android.usbDirect.state.openingSource",
  opening: "player.android.usbDirect.state.opening",
  playing: "player.android.usbDirect.state.playing",
  error: "player.android.usbDirect.state.error",
};

const ANDROID_UNAVAILABLE_MESSAGE: Record<string, MessageKey> = {
  requires_android_14: "player.android.unavailable.requiresAndroid14",
  no_usb_device: "player.android.unavailable.noUsbDevice",
  no_bit_perfect_mixer: "player.android.unavailable.noBitPerfectMixer",
  no_usable_bit_perfect_format: "player.android.unavailable.noUsableBitPerfectFormat",
};

const ANDROID_MIXER_REJECTION_MESSAGE: Record<string, MessageKey> = {
  encoding_unrecognized: "player.android.reject.encoding_unrecognized",
  channel_index_mask: "player.android.reject.channel_index_mask",
  no_channel_mask: "player.android.reject.no_channel_mask",
  invalid_sample_rate: "player.android.reject.invalid_sample_rate",
};

const ANDROID_TRANSPARENCY_REASON_MESSAGE: Record<string, MessageKey> = {
  mixer_not_bit_perfect: "player.android.reason.mixer_not_bit_perfect",
  source_provenance_unknown: "player.android.reason.source_provenance_unknown",
  source_transformed: "player.android.reason.source_transformed",
  source_lossy: "player.android.reason.source_lossy",
  source_precision_unknown: "player.android.reason.source_precision_unknown",
  gain_changed: "player.android.reason.gain_changed",
  rate_changed: "player.android.reason.rate_changed",
  layout_changed: "player.android.reason.layout_changed",
  precision_reduced: "player.android.reason.precision_reduced",
  encoding_changed: "player.android.reason.encoding_changed",
};

const ANDROID_ENCODING_MESSAGE: Record<string, MessageKey> = {
  pcm_16: "player.android.encoding.pcm_16",
  pcm_24: "player.android.encoding.pcm_24",
  pcm_32: "player.android.encoding.pcm_32",
  pcm_float: "player.android.encoding.pcm_float",
};

function timeLabel(milliseconds: number, locale: string): string {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  const minutes = Math.floor(seconds / 60).toLocaleString(locale, { useGrouping: false });
  const remainingSeconds = (seconds % 60).toLocaleString(locale, {
    minimumIntegerDigits: 2,
    useGrouping: false,
  });
  return `${minutes}:${remainingSeconds}`;
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error && error.message ? error.message : fallback;
}

function protocolLabel(protocol: string, t: (key: MessageKey) => string): string {
  switch (protocol) {
    case "upnp":
      return t("player.protocol.upnp");
    case "airplay":
      return t("player.protocol.airplay");
    case "cast":
      return t("player.protocol.cast");
    case "browser":
      return t("player.protocol.browser");
    default:
      return t("player.protocol.unknown");
  }
}

function deviceLabel(device: Device, t: (key: MessageKey) => string, currentBrowserID = ""): string {
  const name = device.id === currentBrowserID ? `${device.name} (${t("player.browser.thisDevice")})` : device.name;
  return `${name} [${protocolLabel(device.protocol, t)}]`;
}

function PlayerIcon({ name }: { name: "play" | "pause" | "stop" | "next" | "previous" | "music" | "refresh" | "expand" | "collapse" | "edit" | "shuffle" | "repeat" | "modes" }) {
  const paths = {
    play: <path d="m8 5 11 7-11 7Z" />,
    pause: <path d="M9 5v14M15 5v14" />,
    stop: <path d="M7 7h10v10H7z" />,
    next: <><path d="m6 6 8 6-8 6Z" /><path d="M18 6v12" /></>,
    previous: <><path d="m18 6-8 6 8 6Z" /><path d="M6 6v12" /></>,
    music: <><path d="M9 18V5l10-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="16" cy="16" r="3" /></>,
    refresh: <><path d="M20 7h-5V2" /><path d="M20 7a8 8 0 1 0 1 7" /></>,
    edit: <><path d="m15 5 4 4M4 20l4-1L20 7a2.8 2.8 0 0 0-4-4L4 15Z" /></>,
    expand: <path d="m7 14 5-5 5 5" />,
    collapse: <path d="m7 10 5 5 5-5" />,
    shuffle: <><path d="M4 7h3c4 0 6 10 10 10h3" /><path d="m17 14 3 3-3 3" /><path d="M4 17h3c1.7 0 3-1.8 4.2-4" /><path d="M15 7h5" /><path d="m17 4 3 3-3 3" /></>,
    repeat: <><path d="M17 2l3 3-3 3" /><path d="M3 11V9a4 4 0 0 1 4-4h13" /><path d="m7 22-3-3 3-3" /><path d="M21 13v2a4 4 0 0 1-4 4H4" /></>,
    modes: <><path d="M4 7h10M18 7h2M4 17h2M10 17h10" /><circle cx="16" cy="7" r="2" /><circle cx="8" cy="17" r="2" /></>,
  };
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">{paths[name]}</svg>;
}

export default function PlayerBar({ revision, phoneExpanded, onPhoneExpandedChange: setPhoneExpanded, onNotice, onStatusWarning, onQueueChange, onShowQueue }: PlayerBarProps) {
  const { locale, t } = useI18n();
  const [player, setPlayer] = useState<PlayerState | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [localBrowserDevice, setLocalBrowserDevice] = useState<Device | null>(null);
  const [pendingLocalReconnect, setPendingLocalReconnect] = useState(false);
  // The last registered local output survives the moment a backend switch releases it.
  const lastLocalOutputID = useRef("");
  useEffect(() => {
    if (localBrowserDevice) lastLocalOutputID.current = localBrowserDevice.id;
  }, [localBrowserDevice]);
  const [browserAutoplayBlocked, setBrowserAutoplayBlocked] = useState(false);
  const [position, setPosition] = useState(0);
  const [seeking, setSeeking] = useState(false);
  const seekingRef = useRef(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [pairingPIN, setPairingPIN] = useState("");
  const [pairingPassword, setPairingPassword] = useState("");
  const [pairingStatus, setPairingStatus] = useState<PairingStatus | null>(null);
  const [pairingError, setPairingError] = useState("");
  const [pairingStarted, setPairingStarted] = useState(false);
  const phoneExpandRef = useRef<HTMLButtonElement>(null);
  const phoneCollapseRef = useRef<HTMLButtonElement>(null);
  const modeChooserButtonRef = useRef<HTMLButtonElement>(null);
  const modeChooserFirstButtonRef = useRef<HTMLButtonElement>(null);
  const [modeChooserOpen, setModeChooserOpen] = useState(false);
  const observedPlayerError = useRef<{ revision: number; message: string } | null>(null);
  const localOutputRef = useRef<LocalOutputHandle>(null);
  const [usesNativeAndroid] = useState(hasNativeAndroidAudio);
  const [windowsBridge] = useState(() => usesNativeAndroid ? null : nativeWindowsAudioBridge());
  const [windowsAudioState, setWindowsAudioState] = useState<NativeWindowsState | null>(null);
  const previousWindowsEnabled = useRef(false);
  const [windowsAudioResolved, setWindowsAudioResolved] = useState(() => windowsBridge === null);
  const [windowsAudioStatusError, setWindowsAudioStatusError] = useState("");
  const [windowsSettingsOpen, setWindowsSettingsOpen] = useState(false);
  const [windowsConfigurationError, setWindowsConfigurationError] = useState("");
  const [windowsConfigurationBusy, setWindowsConfigurationBusy] = useState(false);
  const [androidAudioState, setAndroidAudioState] = useState<NativeAndroidAudioState | null>(null);
  const [androidSettingsOpen, setAndroidSettingsOpen] = useState(false);
  const [androidConfigurationError, setAndroidConfigurationError] = useState("");
  const [androidConfigurationBusy, setAndroidConfigurationBusy] = useState(false);
  const [usbDirectBusy, setUsbDirectBusy] = useState(false);
  const [defaultBrowserName] = useState(() => localOutputName(usesNativeAndroid, windowsBridge !== null));
  const [browserAlias, setBrowserAlias] = useState("");
  const [nameDraft, setNameDraft] = useState("");
  const [nameStorageKey, setNameStorageKey] = useState<string | null>(null);
  const [nameEditorOpen, setNameEditorOpen] = useState(false);
  const [nameSaving, setNameSaving] = useState(false);
  const [nameError, setNameError] = useState("");
  const [localOutputRecovery, setLocalOutputRecovery] = useState({ recovering: false, message: "" });
  const [localVolume, setLocalVolume] = useState<number | null>(null);
  const automaticFallbackAttempted = useRef(false);
  const [outputsLoaded, setOutputsLoaded] = useState(false);
  const browserName = browserAlias || defaultBrowserName;
  const usesNativeOutput = usesNativeAndroid || windowsAudioState?.audio.enabled === true;

  useEffect(() => {
    if (!windowsBridge) return;
    let disposed = false;
    let updateSequence = 0;
    let unsubscribe: (() => void) | null = null;
    try {
      unsubscribe = windowsBridge.subscribe((value) => {
        if (disposed) return;
        updateSequence += 1;
        const next = parseNativeWindowsState(value);
        if (!next) {
          setWindowsAudioStatusError("invalid_state");
          return;
        }
        setWindowsAudioState(next);
        setWindowsAudioStatusError("");
      });
      if (typeof unsubscribe !== "function") throw new Error("invalid_subscription");
    } catch (caught) {
      setWindowsAudioStatusError(caught instanceof Error ? caught.message : "subscription_failed");
      setWindowsAudioResolved(true);
      return () => undefined;
    }

    const requestSequence = updateSequence;
    void requestNativeWindowsAudio(windowsBridge, "status")
      .then((next) => {
        if (disposed) return;
        if (updateSequence === requestSequence) {
          setWindowsAudioState(next);
          setWindowsAudioStatusError("");
        }
      })
      .catch((caught: unknown) => {
        if (!disposed && updateSequence === requestSequence) {
          setWindowsAudioStatusError(caught instanceof Error && caught.message ? caught.message : "status_failed");
        }
      })
      .finally(() => {
        if (!disposed) setWindowsAudioResolved(true);
      });

    return () => {
      disposed = true;
      unsubscribe?.();
    };
  }, [windowsBridge]);

  useEffect(() => {
    const enabled = windowsAudioState?.audio.enabled === true;
    if (previousWindowsEnabled.current && !enabled) {
      setLocalBrowserDevice(null);
      setLocalVolume(null);
      setBrowserAutoplayBlocked(false);
    }
    previousWindowsEnabled.current = enabled;
  }, [windowsAudioState]);

  useEffect(() => {
    const controller = new AbortController();
    api<{ product: string; protocol: number; id: string }>("/discovery", { signal: controller.signal })
      .then((identity) => {
        if (controller.signal.aborted) return;
        if (identity.product !== "jastreamer" || identity.protocol !== 1
          || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(identity.id)) {
          throw new Error(t("player.browser.identityFailed"));
        }
        const key = `jastreamer.browser-output.name.v1:${window.location.origin}:${identity.id}`;
        setNameStorageKey(key);
        try {
          const saved = (window.localStorage.getItem(key) ?? "").trim();
          if (new TextEncoder().encode(saved).length > 80) {
            setNameError(t("player.browser.nameTooLong"));
            return;
          }
          setBrowserAlias(saved);
          setNameDraft(saved);
        } catch {
          setNameError(t("player.browser.nameReadFailed"));
        }
      })
      .catch((caught: unknown) => {
        if (!controller.signal.aborted) setNameError(errorMessage(caught, t("player.browser.identityFailed")));
      });
    return () => controller.abort();
  }, [t]);

  async function saveBrowserName(value: string) {
    if (!nameStorageKey || nameSaving || busy) return;
    const alias = value.trim();
    const nextName = alias || defaultBrowserName;
    if (new TextEncoder().encode(nextName).length > 80) {
      setNameError(t("player.browser.nameTooLong"));
      return;
    }
    setNameSaving(true);
    setNameError("");
    try {
      if (!localOutputRef.current) throw new Error(t("player.browser.registrationFailed"));
      await localOutputRef.current.rename(nextName);
      setBrowserAlias(alias);
      setNameDraft(alias);
      try {
        if (alias) window.localStorage.setItem(nameStorageKey, alias);
        else window.localStorage.removeItem(nameStorageKey);
      } catch {
        setNameError(t("player.browser.nameSaveFailed"));
        return;
      }
      onNotice(t("player.browser.nameSaved"));
    } catch (caught) {
      setNameError(errorMessage(caught, t("common.requestFailed")));
    } finally {
      setNameSaving(false);
    }
  }

  const applyError = useCallback((message: string, playerRevision: number, announce = true) => {
    setError(message);
    if (!message) {
      observedPlayerError.current = null;
      return;
    }
    if (!announce) return;
    if (
      observedPlayerError.current?.revision === playerRevision
      && observedPlayerError.current.message === message
    ) return;

    observedPlayerError.current = { revision: playerRevision, message };
    onNotice(message, true);
  }, [onNotice]);
  const handleLocalOutputError = useCallback((message: string) => {
    onNotice(message, true);
  }, [onNotice]);
  const handleLocalOutputRecovery = useCallback((recovering: boolean, message: string) => {
    setLocalOutputRecovery((current) => (
      current.recovering === recovering && current.message === message ? current : { recovering, message }
    ));
  }, []);

  const load = useCallback(async () => {
    const [playerResult, rendererResult] = await Promise.allSettled([
      api<PlayerState>("/player"),
      api<{ items: Device[] }>("/renderers"),
    ]);
    const messages: string[] = [];
    if (playerResult.status === "fulfilled") {
      const nextPlayer = playerResult.value;
      setPlayer(nextPlayer);
      onStatusWarning(nextPlayer.status_warning ?? null);
      if (!seekingRef.current) setPosition(nextPlayer.position_ms);
      if (nextPlayer.error) messages.push(nextPlayer.error);
    } else {
      messages.push(errorMessage(playerResult.reason, t("common.requestFailed")));
    }
    if (rendererResult.status === "fulfilled") {
      setDevices(rendererResult.value.items ?? []);
      setOutputsLoaded(true);
    } else {
      const message = errorMessage(rendererResult.reason, t("common.requestFailed"));
      if (!messages.includes(message)) messages.push(message);
    }
    applyError(
      messages.join("\n\n"),
      playerResult.status === "fulfilled" ? playerResult.value.revision : -1,
      playerResult.status === "rejected" || rendererResult.status === "rejected",
    );
    setLoading(false);
  }, [applyError, onStatusWarning, t]);

  useEffect(() => {
    void load();
  }, [load, revision]);

  useEffect(() => () => onStatusWarning(null), [onStatusWarning]);

  useEffect(() => {
    if (!isPhone || !phoneExpanded) return;
    const focusFrame = window.requestAnimationFrame(() => phoneCollapseRef.current?.focus());
    return () => window.cancelAnimationFrame(focusFrame);
  }, [phoneExpanded]);

  useEffect(() => {
    if (!isPhone || !modeChooserOpen || phoneExpanded) return;
    const focusFrame = window.requestAnimationFrame(() => modeChooserFirstButtonRef.current?.focus());
    return () => window.cancelAnimationFrame(focusFrame);
  }, [modeChooserOpen, phoneExpanded]);

  useEffect(() => {
    if (!player || player.state !== "playing" || seeking) return;
    const timer = window.setInterval(() => {
      setPosition((current) => Math.min(player.duration_ms || current + 1000, current + 1000));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [player, seeking]);

  const visibleDevices = useMemo(() => {
    if (!localBrowserDevice) return devices;
    return [
      localBrowserDevice,
      ...devices.filter((device) => device.id !== localBrowserDevice.id),
    ];
  }, [devices, localBrowserDevice]);

  const selectedDevice = useMemo(
    () => visibleDevices.find((device) => device.id === player?.renderer_id),
    [visibleDevices, player?.renderer_id],
  );
  const missingSelectedDeviceLabel = player?.renderer_id && !selectedDevice
    ? t("player.disconnectedOutput", {
        protocol: protocolLabel(player.renderer_id.startsWith("renderer-") ? "upnp" : player.renderer_id.split(":", 1)[0], t),
      })
    : "";
  const pairingRequired = Boolean(
    selectedDevice?.protocol === "airplay"
    && (selectedDevice.pairing_required || selectedDevice.password_required),
  );
  const pairingAvailable = player?.state === "stopped" && !player.pending_command;

  useEffect(() => {
    if (automaticFallbackAttempted.current || loading || !outputsLoaded || !player || !nameStorageKey || busy) return;
    if (windowsBridge && (!windowsAudioResolved || !windowsAudioState)) return;
    if (embeddedClient === "ios" || !player.renderer_id || selectedDevice?.online) {
      automaticFallbackAttempted.current = true;
      return;
    }
    if (player.pending_command || !["stopped", "unavailable"].includes(player.state)) return;

    const output = localOutputRef.current;
    if (!output) return;
    automaticFallbackAttempted.current = true;
    const expected = player;
    const alreadyConnected = localBrowserDevice !== null;
    setBusy("output");
    void (async () => {
      let device: Device | null = null;
      try {
        device = await output.connectAutomatically();
        if (!device) return;
        const next = await api<PlayerState>("/player/output/fallback", {
          method: "POST",
          body: JSON.stringify({
            renderer_id: device.id,
            expected_renderer_id: expected.renderer_id,
            expected_revision: expected.revision,
          }),
        });
        setPlayer(next);
        setPosition(next.position_ms);
        onStatusWarning(next.status_warning ?? null);
        applyError(next.error || "", next.revision, false);
      } catch (caught) {
        // Another Control or discovery may have selected/recovered an output.
        // Reconcile even after a lost response: selection may have committed.
        try {
          const current = await api<PlayerState>("/player");
          setPlayer(current);
          setPosition(current.position_ms);
          onStatusWarning(current.status_warning ?? null);
          applyError(current.error || "", current.revision, false);
          if (device && !alreadyConnected && current.renderer_id !== device.id) await output.disconnect();
        } catch {
          // Do not tear down a potentially selected native transport on ambiguity.
        }
        if (!(caught instanceof ApiError && caught.status === 409)) {
          setError(errorMessage(caught, t("common.requestFailed")));
        }
      } finally {
        setBusy("");
      }
    })();
  }, [loading, outputsLoaded, player, nameStorageKey, busy, selectedDevice, localBrowserDevice, applyError, onStatusWarning, t, windowsAudioResolved, windowsAudioState, windowsBridge]);

  async function changeLocalVolume(value: number) {
    if (!localOutputRef.current) return;
    try {
      await localOutputRef.current.setVolume(value);
    } catch (caught) {
      onNotice(errorMessage(caught, t("player.browser.actionFailed")), true);
    }
  }

  async function configureWindowsAudio(configuration: NativeWindowsConfiguration) {
    const output = localOutputRef.current;
    if (!output || !windowsAudioState || windowsConfigurationBusy || busy
      || player?.state !== "stopped" || Boolean(player.pending_command)
      || !windowsAudioState.audio.can_configure) return;
    // Endpoint/exclusive changes keep the same local output. Switching between browser and
    // native audio replaces it, so reselect this device when it was the selected output.
    const reconnect = configuration.enabled !== windowsAudioState.audio.enabled
      && Boolean(player.renderer_id)
      && player.renderer_id === (localBrowserDevice?.id ?? lastLocalOutputID.current);
    setWindowsConfigurationBusy(true);
    setWindowsConfigurationError("");
    setBusy("windows-output");
    try {
      const next = await output.configureWindows(configuration);
      setWindowsAudioState(next);
      setLocalBrowserDevice(next.device);
      setLocalVolume(next.volume);
      if (reconnect) setPendingLocalReconnect(true);
      else onNotice(t("player.windows.configurationSaved"));
    } catch (caught) {
      const message = errorMessage(caught, t("player.windows.actionFailed"));
      setWindowsConfigurationError(message);
      onNotice(message, true);
    } finally {
      setBusy("");
      setWindowsConfigurationBusy(false);
    }
  }

  // The bit-perfect setting keeps the same phone output registration and applies from the
  // next track, so it only replaces the requested path while playback is stopped.
  async function configureAndroidAudio(bitPerfect: boolean) {
    const output = localOutputRef.current;
    if (!output || !androidAudioState || androidConfigurationBusy || busy
      || player?.state !== "stopped" || Boolean(player.pending_command)
      || !androidAudioState.can_configure) return;
    setAndroidConfigurationBusy(true);
    setAndroidConfigurationError("");
    setBusy("android-output");
    try {
      await output.configureAndroid(bitPerfect);
      onNotice(t("player.android.configurationSaved"));
    } catch (caught) {
      const message = errorMessage(caught, t("player.android.actionFailed"));
      setAndroidConfigurationError(message);
      onNotice(message, true);
    } finally {
      setBusy("");
      setAndroidConfigurationBusy(false);
    }
  }

  // Debug builds only: the direct USB test drives the DAC itself, so it neither uses nor
  // blocks the Server playback commands and keeps its own in-flight flag.
  async function runUsbDirect(action: "scan" | "start" | "stop") {
    const output = localOutputRef.current;
    if (!output || usbDirectBusy) return;
    setUsbDirectBusy(true);
    try {
      if (action === "scan") await output.usbDirectScanAndroid();
      else if (action === "start") await output.usbDirectStartAndroid();
      else await output.usbDirectStopAndroid();
    } catch (caught) {
      onNotice(errorMessage(caught, t("player.android.usbDirect.actionFailed")), true);
    } finally {
      setUsbDirectBusy(false);
    }
  }

  // Reconnect only after the reconfigured backend has rendered: the local output handle
  // still points at the previous (browser or native) implementation until then.
  useEffect(() => {
    if (!pendingLocalReconnect || busy) return;
    setPendingLocalReconnect(false);
    void selectOutput("browser:local");
  }, [pendingLocalReconnect, busy]);

  useEffect(() => {
    setPairingPIN("");
    setPairingPassword("");
    setPairingStatus(null);
    setPairingError("");
    setPairingStarted(false);
  }, [selectedDevice?.id]);

  async function command(action: "play" | "pause" | "stop" | "next" | "previous") {
    setBusy(action);
    try {
      const next = await api<PlayerState>("/player", {
        method: "POST",
        body: JSON.stringify({ action }),
      });
      setPlayer(next);
      onStatusWarning(next.status_warning ?? null);
      setPosition(next.position_ms);
      applyError(next.error || "", next.revision);
      onQueueChange();
    } catch (requestError) {
      const message = errorMessage(requestError, t("common.requestFailed"));
      setError(message);
      onNotice(message, true);
    } finally {
      setBusy("");
    }
  }

  async function setMode(change: { shuffle: boolean } | { repeat_mode: RepeatMode }, operation: "shuffle" | "repeat") {
    if (!player || player.pending_command || busy) return;
    setBusy(`mode-${operation}`);
    try {
      const next = await api<PlayerState>("/player/mode", {
        method: "POST",
        body: JSON.stringify(change),
      });
      setPlayer((current) => current && current.revision > next.revision ? current : next);
      onStatusWarning(next.status_warning ?? null);
      applyError(next.error || "", next.revision);
    } catch (requestError) {
      const message = errorMessage(requestError, t("common.requestFailed"));
      setError(message);
      onNotice(message, true);
    } finally {
      setBusy("");
    }
  }

  async function seek() {
    if (!player || !player.capabilities.seek || player.duration_ms <= 0) {
      setSeeking(false);
      seekingRef.current = false;
      return;
    }
    setBusy("seek");
    try {
      const next = await api<PlayerState>("/player", {
        method: "POST",
        body: JSON.stringify({ action: "seek", position_ms: Math.round(position) }),
      });
      setPlayer(next);
      onStatusWarning(next.status_warning ?? null);
      setPosition(next.position_ms);
      applyError(next.error || "", next.revision);
    } catch (requestError) {
      setPosition(player.position_ms);
      const message = errorMessage(requestError, t("common.requestFailed"));
      setError(message);
      onNotice(message, true);
    } finally {
      setBusy("");
      setSeeking(false);
      seekingRef.current = false;
    }
  }

  async function selectOutput(rendererID: string) {
    if (!rendererID) return;
    setBusy("output");
    try {
      if (rendererID === "browser:local") {
        if (!localOutputRef.current) throw new Error(t("player.browser.registrationFailed"));
        rendererID = (await localOutputRef.current.connect()).id;
      }
      const next = await api<PlayerState>("/player/output", {
        method: "PUT",
        body: JSON.stringify({ renderer_id: rendererID }),
      });
      setPlayer(next);
      onStatusWarning(next.status_warning ?? null);
      applyError(next.error || "", next.revision);
      onNotice(t("player.outputChanged"));
    } catch (requestError) {
      const message = errorMessage(requestError, t("common.requestFailed"));
      setError(message);
      onNotice(message, true);
    } finally {
      setBusy("");
    }
  }

  async function sendPairing(request: PairingRequest): Promise<PairingStatus | null> {
    if (!selectedDevice || !pairingRequired || !pairingAvailable || busy) return null;

    setBusy("pairing");
    setPairingError("");
    setPairingStatus({ required: true, prompt: "" });
    try {
      const next = await api<PairingStatus>(
        `/renderers/${encodeURIComponent(selectedDevice.id)}/pairing`,
        {
          method: "POST",
          body: JSON.stringify(request),
        },
      );
      setPairingStatus(next);
      await load();
      if (!next.required) {
        setPairingStarted(false);
        setDevices((current) => current.map((device) => (
          device.id === selectedDevice.id
            ? { ...device, pairing_required: false, password_required: false }
            : device
        )));
        onNotice(t("player.pairing.complete", { device: deviceLabel(selectedDevice, t) }));
      }
      return next;
    } catch (requestError) {
      const message = errorMessage(requestError, t("common.requestFailed"));
      setPairingStatus(null);
      setPairingError(message);
      onNotice(message, true);
      return null;
    } finally {
      setPairingPIN("");
      setPairingPassword("");
      setBusy("");
    }
  }

  async function beginPairing() {
    const next = await sendPairing({});
    setPairingStarted(Boolean(next?.required));
  }

  async function pairDevice(event: FormEvent) {
    event.preventDefault();
    if (!selectedDevice || (selectedDevice.pairing_required && !pairingStarted)) return;

    const request: PairingRequest = {};
    if (selectedDevice.pairing_required) request.pin = pairingPIN.trim();
    if (selectedDevice.password_required) request.password = pairingPassword;
    const next = await sendPairing(request);
    if (!next && selectedDevice.pairing_required) setPairingStarted(false);
  }

  async function refreshDevices() {
    setBusy("refresh");
    try {
      const [, nativeState] = await Promise.all([
        api<void>("/renderers/refresh", {
          method: "POST",
          body: JSON.stringify({}),
        }),
        windowsBridge ? requestNativeWindowsAudio(windowsBridge, "status") : Promise.resolve(null),
      ]);
      if (nativeState) {
        setWindowsAudioState(nativeState);
        setWindowsAudioResolved(true);
        setWindowsAudioStatusError("");
      }
      onNotice(t("player.refreshingOutputs"));
      window.setTimeout(() => void load(), 1200);
    } catch (requestError) {
      onNotice(errorMessage(requestError, t("common.requestFailed")), true);
    } finally {
      setBusy("");
    }
  }

  const canControl = Boolean(player?.renderer_id) && !player?.pending_command;
  const canUsePlayback = canControl && !pairingRequired;
  const canPause = player?.state === "playing" && player.capabilities.pause;
  const browserRetry = !usesNativeOutput && browserAutoplayBlocked && selectedDevice?.id === localBrowserDevice?.id;
  const playing = player?.state === "playing" || player?.state === "starting";
  const duration = player?.duration_ms || player?.track?.duration_ms || 0;
  const repeatMode = player?.repeat_mode ?? "off";
  const shuffleLabel = t(player?.shuffle ? "player.mode.shuffle" : "player.mode.sequential");
  const repeatLabel = t(
    repeatMode === "all"
      ? "player.mode.repeatAll"
      : repeatMode === "one"
        ? "player.mode.repeatOne"
        : "player.mode.repeatOff",
  );
  const nextRepeatMode: RepeatMode = repeatMode === "off" ? "all" : repeatMode === "all" ? "one" : "off";
  const modeDisabled = !player || Boolean(player.pending_command) || Boolean(busy);
  const compactModeLabel = t("player.mode.open", { shuffle: shuffleLabel, repeat: repeatLabel });
  const windowsCanConfigure = Boolean(
    windowsAudioState?.audio.can_configure
    && player?.state === "stopped"
    && !player.pending_command
    && !busy
    && !nameSaving
    && !windowsConfigurationBusy,
  );
  // Windows audio settings only apply while this device is the selected output; keep them
  // visible through a reconfiguration and its automatic reconnection.
  const windowsSettingsAvailable = Boolean(windowsBridge && (
    windowsConfigurationBusy || pendingLocalReconnect || busy === "output"
    || (localBrowserDevice && localBrowserDevice.id === player?.renderer_id)
  ));
  const windowsStatusError = ["invalid_state", "invalid_subscription", "subscription_failed", "status_failed"].includes(windowsAudioStatusError)
    ? t("player.windows.statusFailed")
    : windowsAudioStatusError;
  const windowsRequestedEndpoint = windowsAudioState?.audio.devices.find(
    (device) => device.id === windowsAudioState.audio.requested.device_id,
  );
  const androidCanConfigure = Boolean(
    androidAudioState?.can_configure
    && player?.state === "stopped"
    && !player.pending_command
    && !busy
    && !nameSaving
    && !androidConfigurationBusy,
  );
  // Phone audio settings only apply while this device is the selected output.
  const androidSettingsAvailable = Boolean(usesNativeAndroid && (
    androidConfigurationBusy || busy === "output"
    || (localBrowserDevice && localBrowserDevice.id === player?.renderer_id)
  ));
  const androidUsbDevice = androidAudioState?.devices[0];
  const androidActual = androidAudioState?.actual ?? null;
  // Release builds never report debug_build, so the test section cannot be rendered there.
  const usbDirect = androidAudioState?.debug_build === true ? androidAudioState.usb_direct : null;

  function shuffleButton(labeled = false, compactChooser = false) {
    return (
      <button
        ref={compactChooser ? modeChooserFirstButtonRef : undefined}
        className={`icon-button player-mode-button player-mode-shuffle${player?.shuffle ? " is-active" : ""}${labeled ? " player-mode-button-labeled" : ""}`}
        type="button"
        data-player-mode="shuffle"
        aria-label={shuffleLabel}
        aria-pressed={player?.shuffle ?? false}
        aria-busy={busy === "mode-shuffle"}
        title={shuffleLabel}
        disabled={modeDisabled}
        onClick={() => void setMode({ shuffle: !player?.shuffle }, "shuffle")}
      >
        <span className="player-mode-icon"><PlayerIcon name="shuffle" /></span>
        {labeled && <span>{shuffleLabel}</span>}
      </button>
    );
  }

  function repeatButton(labeled = false) {
    return (
      <button
        className={`icon-button player-mode-button player-mode-repeat${repeatMode !== "off" ? " is-active" : ""}${labeled ? " player-mode-button-labeled" : ""}`}
        type="button"
        data-player-mode="repeat"
        data-repeat-mode={repeatMode}
        aria-label={repeatLabel}
        aria-busy={busy === "mode-repeat"}
        title={repeatLabel}
        disabled={modeDisabled}
        onClick={() => void setMode({ repeat_mode: nextRepeatMode }, "repeat")}
      >
        <span className="player-mode-icon">
          <PlayerIcon name="repeat" />
          {repeatMode === "one" && <span className="repeat-one-indicator" aria-hidden="true">1</span>}
        </span>
        {labeled && <span>{repeatLabel}</span>}
      </button>
    );
  }

  function modeControls(surface: "phone-expanded" | "phone-compact") {
    return (
      <div
        className={`player-mode-controls player-mode-controls-${surface}`}
        role="group"
        aria-label={t("player.mode.heading")}
        aria-busy={busy === "mode-shuffle" || busy === "mode-repeat"}
      >
        {shuffleButton(true, surface === "phone-compact")}
        {repeatButton(true)}
      </div>
    );
  }
  const localOutputAdapter = usesNativeAndroid || !windowsBridge || (windowsAudioResolved && windowsAudioState) ? (
    <LocalOutput
      ref={localOutputRef}
      name={browserName}
      disconnectedError={t("player.browser.disconnected")}
      registrationError={t("player.browser.registrationFailed")}
      actionError={t("player.browser.actionFailed")}
      bridgeError={usesNativeAndroid ? t("player.native.bridgeFailed") : t("player.windows.bridgeFailed")}
      windowsBridge={windowsBridge}
      windowsState={windowsAudioState}
      onWindowsStateChange={setWindowsAudioState}
      onAndroidAudioChange={setAndroidAudioState}
      onDeviceChange={setLocalBrowserDevice}
      onAutoplayBlocked={setBrowserAutoplayBlocked}
      onRecoveryChange={handleLocalOutputRecovery}
      onError={handleLocalOutputError}
      onVolumeChange={setLocalVolume}
    />
  ) : null;
  const outputPicker = (
    <div className="output-picker">
      <label htmlFor="player-output">{t("player.output")}</label>
      <div className="output-control">
        <select
          id="player-output"
          value={player?.renderer_id || ""}
          disabled={player?.state !== "stopped" || Boolean(player?.pending_command) || Boolean(busy) || nameSaving}
          onChange={(event) => void selectOutput(event.target.value)}
        >
          <option value="">{t("player.selectOutput")}</option>
          {!localBrowserDevice && (
            <option
              value="browser:local"
              disabled={!nameStorageKey || Boolean(windowsBridge && (!windowsAudioResolved || !windowsAudioState))}
            >
              {browserName} ({t("player.browser.thisDevice")}) [{t("player.protocol.browser")}]
            </option>
          )}
          {missingSelectedDeviceLabel && player?.renderer_id && (
            <option value={player.renderer_id} disabled>{missingSelectedDeviceLabel}</option>
          )}
          {visibleDevices.map((device) => (
            <option key={device.id} value={device.id} disabled={!device.online}>
              {deviceLabel(device, t, localBrowserDevice?.id)}{device.online ? "" : ` (${t("player.offline")})`}
            </option>
          ))}
        </select>
        <button
          className="icon-button"
          type="button"
          aria-label={t("player.refreshOutputs")}
          disabled={Boolean(busy)}
          onClick={() => void refreshDevices()}
        >
          <PlayerIcon name="refresh" />
        </button>
        <button
          className="icon-button"
          type="button"
          aria-label={t("player.browser.editName")}
          title={t("player.browser.editName")}
          aria-expanded={nameEditorOpen}
          aria-controls="browser-name-panel"
          onClick={() => {
            setWindowsSettingsOpen(false);
            setAndroidSettingsOpen(false);
            setNameEditorOpen((current) => !current);
          }}
        >
          <PlayerIcon name="edit" />
        </button>
        {windowsSettingsAvailable && (
          <button
            className="icon-button"
            type="button"
            aria-label={t("player.windows.openSettings")}
            title={t("player.windows.openSettings")}
            aria-expanded={windowsSettingsOpen}
            aria-controls="windows-audio-panel"
            onClick={() => {
              setNameEditorOpen(false);
              setWindowsSettingsOpen((current) => !current);
            }}
          >
            <PlayerIcon name="modes" />
          </button>
        )}
        {androidSettingsAvailable && (
          <button
            className="icon-button"
            type="button"
            aria-label={t("player.android.openSettings")}
            title={t("player.android.openSettings")}
            aria-expanded={androidSettingsOpen}
            aria-controls="android-audio-panel"
            onClick={() => {
              setNameEditorOpen(false);
              setAndroidSettingsOpen((current) => !current);
            }}
          >
            <PlayerIcon name="modes" />
          </button>
        )}
      </div>
      {localBrowserDevice && localBrowserDevice.id === player?.renderer_id && localVolume !== null && (
        <label className="local-volume-control">
          <span>{t("player.localVolume")}</span>
          <input
            type="range"
            min={0}
            max={100}
            step={1}
            value={Math.round(localVolume * 100)}
            aria-label={t("player.localVolume")}
            aria-valuetext={`${Math.round(localVolume * 100)}%`}
            disabled={Boolean(busy)}
            onChange={(event) => void changeLocalVolume(Number(event.target.value) / 100)}
          />
          <output>{Math.round(localVolume * 100)}%</output>
        </label>
      )}
      {nameEditorOpen && (
        <section className="pairing-panel browser-name-panel" id="browser-name-panel" aria-labelledby="browser-name-heading">
          <h2 id="browser-name-heading">{t("player.browser.editName")}</h2>
          <p className="muted">{t("player.browser.nameHelp")}</p>
          <form className="browser-name-form" onSubmit={(event) => { event.preventDefault(); void saveBrowserName(nameDraft); }}>
            <label htmlFor="browser-output-alias">{t("player.browser.alias")}</label>
            <input id="browser-output-alias" className="input" value={nameDraft} placeholder={defaultBrowserName} maxLength={80} disabled={nameSaving} onChange={(event) => setNameDraft(event.target.value)} />
            <p className="muted">{t("player.browser.defaultName", { name: defaultBrowserName })}</p>
            <div className="browser-name-actions">
              <button className="button button-primary" type="submit" disabled={!nameStorageKey || nameSaving || Boolean(busy)}>{t(nameSaving ? "common.saving" : "common.save")}</button>
              <button className="button button-ghost" type="button" disabled={!nameStorageKey || nameSaving || Boolean(busy)} onClick={() => void saveBrowserName("")}>{t("player.browser.useDefaultName")}</button>
              <button className="button button-ghost" type="button" onClick={() => setNameEditorOpen(false)}>{t("common.close")}</button>
            </div>
          </form>
          {nameError && <p className="error-text" role="alert">{nameError}</p>}
        </section>
      )}
      {windowsSettingsAvailable && windowsSettingsOpen && (
        <section
          className="pairing-panel native-audio-panel"
          id="windows-audio-panel"
          aria-labelledby="windows-audio-heading"
          aria-busy={!windowsAudioResolved || windowsConfigurationBusy}
        >
          <div className="native-audio-heading">
            <div>
              <h2 id="windows-audio-heading">{t("player.windows.title")}</h2>
              <p className="muted">{t("player.windows.description")}</p>
            </div>
            <button className="button button-ghost" type="button" onClick={() => setWindowsSettingsOpen(false)}>
              {t("common.close")}
            </button>
          </div>
          {windowsAudioState && windowsStatusError && (
            <p className="error-text" role="alert">{windowsStatusError}</p>
          )}
          {!windowsAudioResolved && <p className="muted" role="status">{t("player.windows.loading")}</p>}
          {windowsAudioResolved && !windowsAudioState && (
            <p className="error-text" role="alert">{windowsStatusError || t("player.windows.statusFailed")}</p>
          )}
          {windowsAudioState && (
            <>
              <div className="native-audio-fields">
                <label>
                  <span className="field-label">{t("player.windows.backend")}</span>
                  <select
                    value={windowsAudioState.audio.enabled ? "native" : "browser"}
                    disabled={!windowsCanConfigure}
                    onChange={(event) => void configureWindowsAudio({
                      ...windowsAudioState.audio.requested,
                      enabled: event.target.value === "native",
                    })}
                  >
                    <option value="browser">{t("player.windows.backend.browser")}</option>
                    <option value="native">{t("player.windows.backend.native")}</option>
                  </select>
                </label>
                <label>
                  <span className="field-label">{t("player.windows.endpoint")}</span>
                  <select
                    value={windowsAudioState.audio.requested.device_id}
                    disabled={!windowsCanConfigure || !windowsAudioState.audio.available || !windowsAudioState.audio.enabled}
                    onChange={(event) => void configureWindowsAudio({
                      enabled: windowsAudioState.audio.enabled,
                      device_id: event.target.value,
                      exclusive: windowsAudioState.audio.requested.exclusive,
                    })}
                  >
                    <option value="default">
                      {t("player.windows.endpoint.default", {
                        name: windowsAudioState.audio.devices.find((device) => device.is_default)?.name || t("player.windows.endpoint.system"),
                      })}
                    </option>
                    {windowsAudioState.audio.requested.device_id !== "default"
                      && !windowsAudioState.audio.devices.some((device) => device.id === windowsAudioState.audio.requested.device_id) && (
                        <option value={windowsAudioState.audio.requested.device_id} disabled>
                          {t("player.windows.endpoint.unavailable", { id: windowsAudioState.audio.requested.device_id })}
                        </option>
                      )}
                    {windowsAudioState.audio.devices.filter((device) => device.id !== "default").map((device) => (
                      <option key={device.id} value={device.id}>
                        {device.name}{device.is_default ? ` (${t("player.windows.endpoint.currentDefault")})` : ""}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  <span className="field-label">{t("player.windows.exclusive")}</span>
                  <select
                    value={windowsAudioState.audio.requested.exclusive ? "on" : "off"}
                    disabled={!windowsCanConfigure || !windowsAudioState.audio.available || !windowsAudioState.audio.enabled}
                    onChange={(event) => void configureWindowsAudio({
                      enabled: windowsAudioState.audio.enabled,
                      device_id: windowsAudioState.audio.requested.device_id,
                      exclusive: event.target.value === "on",
                    })}
                  >
                    <option value="off">{t("player.windows.off")}</option>
                    <option value="on">{t("player.windows.on")}</option>
                  </select>
                </label>
              </div>
              {windowsAudioState.audio.available && !windowsAudioState.audio.enabled && (
                <p className="field-help">{t("player.windows.browserFixed")}</p>
              )}
              {!windowsAudioState.audio.available && (
                <p className="error-text" role="status">{t("player.windows.unavailable")}</p>
              )}
              {!windowsCanConfigure && windowsAudioState.audio.available && (
                <p className="field-help">{t("player.windows.stopToConfigure")}</p>
              )}
              <div className="native-audio-status">
                <h3>{t("player.windows.requested")}</h3>
                <dl>
                  <dt>{t("player.windows.endpoint")}</dt>
                  <dd>{windowsAudioState.audio.requested.device_id === "default"
                    ? t("player.windows.endpoint.system")
                    : windowsRequestedEndpoint
                      ? t("player.windows.endpoint.value", {
                          name: windowsRequestedEndpoint.name,
                          id: windowsRequestedEndpoint.id,
                        })
                      : windowsAudioState.audio.requested.device_id}</dd>
                  <dt>{t("player.windows.mode")}</dt>
                  <dd>{t(windowsAudioState.audio.requested.exclusive
                    ? "player.windows.mode.exclusiveRequested"
                    : "player.windows.mode.sharedRequested")}</dd>
                </dl>
                <h3>{t("player.windows.actual")}</h3>
                <dl>
                  <dt>{t("player.windows.engineState")}</dt>
                  <dd>{t(WINDOWS_AUDIO_STATE_MESSAGE[windowsAudioState.audio.state])}</dd>
                  {windowsAudioState.audio.actual ? (
                    <>
                      <dt>{t("player.windows.endpoint")}</dt>
                      <dd>{t("player.windows.endpoint.value", {
                        name: windowsAudioState.audio.actual.name,
                        id: windowsAudioState.audio.actual.device_id,
                      })}</dd>
                      <dt>{t("player.windows.mode")}</dt>
                      <dd>{t(windowsAudioState.audio.actual.mode === "exclusive"
                        ? "player.windows.mode.exclusiveActual"
                        : "player.windows.mode.sharedActual")}</dd>
                      <dt>{t("player.windows.format")}</dt>
                      <dd>{t("player.windows.formatValue", {
                        rate: windowsAudioState.audio.actual.sample_rate.toLocaleString(locale),
                        channels: windowsAudioState.audio.actual.channels,
                        container: windowsAudioState.audio.actual.container_bits,
                        valid: windowsAudioState.audio.actual.valid_bits,
                      })}</dd>
                    </>
                  ) : (
                    <>
                      <dt>{t("player.windows.format")}</dt>
                      <dd>{t("player.windows.noActualFormat")}</dd>
                    </>
                  )}
                </dl>
                {windowsAudioState.audio.actual && (
                  <div className={windowsAudioState.audio.actual.bit_transparent
                    ? "native-transparency native-transparency-qualified"
                    : "native-transparency"}>
                    <strong>{t(windowsAudioState.audio.actual.bit_transparent
                      ? "player.windows.transparency.qualified"
                      : "player.windows.transparency.notVerified")}</strong>
                    <p>{windowsAudioState.audio.actual.bit_transparent
                      ? t("player.windows.transparency.qualifiedDetail")
                      : t("player.windows.transparency.reason", {
                          reason: windowsAudioState.audio.actual.reason || t("player.windows.transparency.unknown"),
                        })}</p>
                  </div>
                )}
              </div>
              {windowsAudioState.error && (
                <p className="error-text native-audio-error" role="alert">
                  {t("player.windows.error", {
                    code: windowsAudioState.error.code,
                    message: windowsAudioState.error.message,
                  })}
                </p>
              )}
            </>
          )}
          {windowsConfigurationError && <p className="error-text" role="alert">{windowsConfigurationError}</p>}
        </section>
      )}
      {androidSettingsAvailable && androidSettingsOpen && (
        <section
          className="pairing-panel native-audio-panel"
          id="android-audio-panel"
          aria-labelledby="android-audio-heading"
          aria-busy={androidConfigurationBusy}
        >
          <div className="native-audio-heading">
            <div>
              <h2 id="android-audio-heading">{t("player.android.title")}</h2>
              <p className="muted">{t("player.android.description")}</p>
            </div>
            <button className="button button-ghost" type="button" onClick={() => setAndroidSettingsOpen(false)}>
              {t("common.close")}
            </button>
          </div>
          {!androidAudioState && <p className="muted" role="status">{t("player.android.loading")}</p>}
          {androidAudioState && (
            <>
              <div className="native-audio-fields">
                <label>
                  <span className="field-label">{t("player.android.bitPerfect")}</span>
                  <select
                    value={androidAudioState.requested.bit_perfect ? "on" : "off"}
                    disabled={!androidCanConfigure || (!androidAudioState.available && !androidAudioState.enabled)}
                    onChange={(event) => void configureAndroidAudio(event.target.value === "on")}
                  >
                    <option value="off">{t("player.android.off")}</option>
                    <option value="on">{t("player.android.on")}</option>
                  </select>
                </label>
              </div>
              <p className="field-help">{t("player.android.serverOnly")}</p>
              {androidAudioState.requested.bit_perfect && (
                <p className="field-help">{t("player.android.fixedVolume")}</p>
              )}
              <p className="field-help">{t("player.android.noFallback")}</p>
              {!androidAudioState.available && (
                <p className="error-text" role="status">
                  {t("player.android.unavailable", {
                    reason: t(ANDROID_UNAVAILABLE_MESSAGE[androidAudioState.reason] ?? "player.android.unavailable.unknown"),
                  })}
                </p>
              )}
              {!androidCanConfigure && androidAudioState.available && (
                <p className="field-help">{t("player.android.stopToConfigure")}</p>
              )}
              <div className="native-audio-status">
                <h3>{t("player.android.requested")}</h3>
                <dl>
                  <dt>{t("player.android.endpoint")}</dt>
                  <dd>{androidUsbDevice?.name || t("player.android.endpoint.none")}</dd>
                  <dt>{t("player.android.mode")}</dt>
                  <dd>{t(androidAudioState.requested.bit_perfect
                    ? "player.android.mode.bitPerfectRequested"
                    : "player.android.mode.standardRequested")}</dd>
                </dl>
                <h3>{t("player.android.actual")}</h3>
                <dl>
                  <dt>{t("player.android.engineState")}</dt>
                  <dd>{t(ANDROID_AUDIO_STATE_MESSAGE[androidAudioState.state])}</dd>
                  {androidActual ? (
                    <>
                      <dt>{t("player.android.endpoint")}</dt>
                      <dd>{androidActual.name || t("player.android.endpoint.system")}</dd>
                      <dt>{t("player.android.mode")}</dt>
                      <dd>{t(androidActual.mode === "bit_perfect"
                        ? "player.android.mode.bitPerfectActual"
                        : "player.android.mode.mixedActual")}</dd>
                      <dt>{t("player.android.format")}</dt>
                      <dd>{t("player.android.formatValue", {
                        rate: androidActual.sample_rate.toLocaleString(locale),
                        channels: androidActual.channels,
                        container: androidActual.container_bits,
                        valid: androidActual.valid_bits,
                        encoding: t(ANDROID_ENCODING_MESSAGE[androidActual.encoding] ?? "player.android.encoding.unknown"),
                      })}</dd>
                    </>
                  ) : (
                    <>
                      <dt>{t("player.android.format")}</dt>
                      <dd>{t("player.android.noActualFormat")}</dd>
                    </>
                  )}
                </dl>
                {androidActual && (
                  <div className={androidActual.bit_transparent
                    ? "native-transparency native-transparency-qualified"
                    : "native-transparency"}>
                    <strong>{t(androidActual.bit_transparent
                      ? "player.android.transparency.qualified"
                      : "player.android.transparency.notVerified")}</strong>
                    <p>{androidActual.bit_transparent
                      ? t("player.android.transparency.qualifiedDetail")
                      : t("player.android.transparency.reason", {
                          reason: t(ANDROID_TRANSPARENCY_REASON_MESSAGE[androidActual.reason] ?? "player.android.reason.unknown"),
                        })}</p>
                  </div>
                )}
              </div>
            </>
          )}
          {androidAudioState && (
            <details className="native-audio-report">
              <summary>{t("player.android.mixerReport")}</summary>
              <p className="muted">{t("player.android.mixerReport.apiLevel", { level: androidAudioState.api_level })}</p>
              {androidAudioState.mixer_report.length === 0 && (
                <p className="muted">{t("player.android.mixerReport.empty")}</p>
              )}
              {androidAudioState.mixer_report.map((report) => (
                <div key={report.device_id}>
                  <h4>{t("player.android.mixerReport.device", {
                    name: report.device_name,
                    type: report.device_type,
                    id: report.device_id,
                  })}</h4>
                  <p className="muted">{t("player.android.mixerReport.counts", {
                    total: report.total,
                    bitPerfect: report.bit_perfect,
                    usable: report.usable_bit_perfect,
                    rejected: report.rejected,
                  })}</p>
                  {report.entries.length === 0
                    ? <p className="muted">{t("player.android.mixerReport.empty")}</p>
                    : (
                      <ul>
                        {report.entries.map((entry, index) => (
                          <li key={`${report.device_id}-${index}`}>
                            {t("player.android.mixerReport.entry", {
                              behavior: t(entry.bit_perfect
                                ? "player.android.mixerReport.bitPerfect"
                                : "player.android.mixerReport.default"),
                              encoding: entry.encoding,
                              label: entry.encoding_label ? ` (${entry.encoding_label})` : "",
                              rate: entry.sample_rate.toLocaleString(locale),
                              mask: `0x${(entry.channel_mask >>> 0).toString(16)}`,
                              indexMask: `0x${(entry.channel_index_mask >>> 0).toString(16)}`,
                            })}
                            {" — "}
                            {entry.rejection
                              ? t(ANDROID_MIXER_REJECTION_MESSAGE[entry.rejection] ?? "player.android.reject.unknown")
                              : t(entry.bit_perfect
                                ? "player.android.mixerReport.usable"
                                : "player.android.mixerReport.notBitPerfect")}
                          </li>
                        ))}
                      </ul>
                    )}
                </div>
              ))}
            </details>
          )}
          {usbDirect && (
            <section className="native-audio-status" aria-labelledby="android-usb-direct-heading">
              <h3 id="android-usb-direct-heading">{t("player.android.usbDirect.title")}</h3>
              <p className="field-help">{t("player.android.usbDirect.warning")}</p>
              <dl>
                <dt>{t("player.android.usbDirect.device")}</dt>
                <dd>
                  {usbDirect.device_name || t("player.android.usbDirect.noDevice")}
                  {usbDirect.uac_version > 0 && ` · ${t("player.android.usbDirect.uac", {
                    version: usbDirect.uac_version,
                    speed: t(usbDirect.high_speed
                      ? "player.android.usbDirect.highSpeed"
                      : "player.android.usbDirect.fullSpeed"),
                  })}`}
                </dd>
              </dl>
              <h4>{t("player.android.usbDirect.capabilities")}</h4>
              {usbDirect.formats.length === 0
                ? <p className="muted">{t("player.android.usbDirect.noFormats")}</p>
                : (
                  <ul>
                    {usbDirect.formats.map((format, index) => (
                      <li key={index}>
                        {t("player.android.usbDirect.format", {
                          bits: format.bits,
                          subslot: format.subslot_bytes,
                          channels: format.channels,
                          sync: format.sync,
                          feedback: t(format.feedback
                            ? "player.android.usbDirect.feedback"
                            : "player.android.usbDirect.noFeedback"),
                        })}
                        {" — "}
                        {format.rates.length === 0
                          ? t("player.android.usbDirect.noRates")
                          : t("player.android.usbDirect.rates", {
                            rates: format.rates.map((rate) => rate.toLocaleString(locale)).join(", "),
                          })}
                      </li>
                    ))}
                  </ul>
                )}
              <div className="native-audio-fields">
                <button
                  className="button button-primary"
                  type="button"
                  disabled={usbDirectBusy || USB_DIRECT_ACTIVE.has(usbDirect.state)}
                  onClick={() => void runUsbDirect("scan")}
                >
                  {t("player.android.usbDirect.scan")}
                </button>
                <button
                  className="button button-primary"
                  type="button"
                  disabled={usbDirectBusy || !usbDirect.can_start || usbDirect.state !== "idle"}
                  onClick={() => void runUsbDirect("start")}
                >
                  {t("player.android.usbDirect.start")}
                </button>
                <button
                  className="button button-ghost"
                  type="button"
                  disabled={usbDirectBusy || !USB_DIRECT_ACTIVE.has(usbDirect.state)}
                  onClick={() => void runUsbDirect("stop")}
                >
                  {t("player.android.usbDirect.stop")}
                </button>
              </div>
              <p className="field-help">
                {usbDirect.track
                  ? t("player.android.usbDirect.track", { track: usbDirect.track })
                  : t("player.android.usbDirect.noTrack")}
              </p>
              <dl>
                <dt>{t("player.android.usbDirect.engineState")}</dt>
                <dd>{t(ANDROID_USB_DIRECT_STATE_MESSAGE[usbDirect.state])}</dd>
                <dt>{t("player.android.usbDirect.requested")}</dt>
                <dd>{usbDirect.requested.sample_rate > 0
                  ? t("player.android.usbDirect.requestedValue", {
                    rate: usbDirect.requested.sample_rate.toLocaleString(locale),
                    bits: usbDirect.requested.bits,
                    channels: usbDirect.requested.channels,
                  })
                  : t("player.android.usbDirect.none")}</dd>
                <dt>{t("player.android.usbDirect.actual")}</dt>
                <dd>{usbDirect.actual.sample_rate > 0
                  ? t("player.android.usbDirect.actualValue", {
                    rate: usbDirect.actual.sample_rate.toLocaleString(locale),
                    bits: usbDirect.actual.bits,
                    subslot: usbDirect.actual.subslot_bytes,
                    channels: usbDirect.actual.channels,
                    sync: usbDirect.actual.sync,
                  })
                  : t("player.android.usbDirect.none")}</dd>
                {usbDirect.decoder && (
                  <>
                    <dt>{t("player.android.usbDirect.decoder")}</dt>
                    <dd>{usbDirect.decoder_encoding
                      ? t("player.android.usbDirect.decoderValue", {
                        decoder: usbDirect.decoder,
                        encoding: usbDirect.decoder_encoding,
                      })
                      : usbDirect.decoder}</dd>
                  </>
                )}
                <dt>{t("player.android.usbDirect.packets")}</dt>
                <dd>{usbDirect.packets.toLocaleString(locale)}</dd>
                <dt>{t("player.android.usbDirect.underruns")}</dt>
                <dd>{usbDirect.underruns.toLocaleString(locale)}</dd>
                <dt>{t("player.android.usbDirect.feedbackRate")}</dt>
                <dd>{usbDirect.feedback_rate > 0
                  ? t("player.android.usbDirect.hz", { rate: usbDirect.feedback_rate.toLocaleString(locale) })
                  : t("player.android.usbDirect.none")}</dd>
              </dl>
              {usbDirect.error && (
                <p className="error-text native-audio-error" role="alert">
                  {t("player.android.usbDirect.error", {
                    code: usbDirect.error.code,
                    message: usbDirect.error.message,
                  })}
                </p>
              )}
            </section>
          )}
          {androidAudioState?.error && (
            <p className="error-text native-audio-error" role="alert">
              {t("player.android.error", {
                code: androidAudioState.error.code,
                message: androidAudioState.error.message,
              })}
            </p>
          )}
          {androidConfigurationError && <p className="error-text" role="alert">{androidConfigurationError}</p>}
        </section>
      )}
      {pairingRequired && selectedDevice && !nameEditorOpen && !windowsSettingsOpen && !androidSettingsOpen && (
        <section className="pairing-panel" aria-labelledby="pairing-heading" aria-busy={busy === "pairing"}>
          <h2 id="pairing-heading">{t("player.pairing.heading", { device: deviceLabel(selectedDevice, t) })}</h2>
          <p className="pairing-prompt" aria-live="polite">
            {pairingStatus?.prompt || (
              busy === "pairing"
                ? t("player.pairing.checking")
                : selectedDevice.pairing_required && !pairingStarted
                  ? t("player.pairing.startHint")
                  : selectedDevice.pairing_required && selectedDevice.password_required
                    ? t("player.pairing.pinAndPasswordPrompt")
                    : selectedDevice.pairing_required
                      ? t("player.pairing.pinPrompt")
                      : t("player.pairing.passwordPrompt")
            )}
          </p>
          {pairingAvailable ? (
            selectedDevice.pairing_required && !pairingStarted ? (
              <div className="pairing-start">
                <button
                  className="button button-primary"
                  type="button"
                  disabled={Boolean(busy)}
                  onClick={() => void beginPairing()}
                >
                  {busy === "pairing" ? t("player.pairing.starting") : t("player.pairing.start")}
                </button>
              </div>
            ) : (
              <form className="pairing-form" autoComplete="off" onSubmit={(event) => void pairDevice(event)}>
                {selectedDevice.pairing_required && (
                  <label>
                    <span className="field-label">{t("player.pairing.pin")}</span>
                    <input
                      className="input"
                      type="text"
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      spellCheck={false}
                      value={pairingPIN}
                      required
                      disabled={Boolean(busy)}
                      onChange={(event) => setPairingPIN(event.target.value)}
                    />
                  </label>
                )}
                {selectedDevice.password_required && (
                  <label>
                    <span className="field-label">{t("player.pairing.password")}</span>
                    <input
                      className="input"
                      type="password"
                      autoComplete="off"
                      spellCheck={false}
                      value={pairingPassword}
                      required
                      disabled={Boolean(busy)}
                      onChange={(event) => setPairingPassword(event.target.value)}
                    />
                  </label>
                )}
                <button className="button button-primary" type="submit" disabled={Boolean(busy)}>
                  {busy === "pairing" ? t("player.pairing.inProgress") : t("player.pairing.submit")}
                </button>
              </form>
            )
          ) : (
            <p className="pairing-stopped-message">{t("player.pairing.stoppedOnly")}</p>
          )}
          {pairingError && <p className="error-text pairing-error" role="alert">{pairingError}</p>}
        </section>
      )}
      {playing && <span className="playing-indicator">{t("player.playing")}</span>}
      {localOutputRecovery.recovering && (
        <span className="local-output-recovery" role="status">
          {localOutputRecovery.message
            ? t("player.native.recoveringDetail", { message: localOutputRecovery.message })
            : t("player.native.recovering")}
        </span>
      )}
      {browserAutoplayBlocked && selectedDevice?.id === localBrowserDevice?.id && (
        <div className="browser-autoplay" role="alert">
          <span>{t("player.browser.autoplayBlocked")}</span>
          <button
            className="button button-primary"
            type="button"
            onClick={() => localOutputRef.current?.retryPlayback?.()}
          >
            {t("player.browser.allowPlayback")}
          </button>
        </div>
      )}
    </div>
  );

  function closeModeChooser() {
    setModeChooserOpen(false);
    window.requestAnimationFrame(() => modeChooserButtonRef.current?.focus());
  }

  function collapsePhonePlayer() {
    setModeChooserOpen(false);
    setPhoneExpanded(false);
    window.requestAnimationFrame(() => phoneExpandRef.current?.focus());
  }

  if (isPhone) {
    return (
      <footer
        className="player-bar phone-player-bar"
        aria-label={t("player.nowPlaying")}
        onKeyDown={(event) => {
          if (event.key !== "Escape" || event.defaultPrevented) return;
          if (!phoneExpanded && !modeChooserOpen) return;
          event.preventDefault();
          event.stopPropagation();
          if (modeChooserOpen) closeModeChooser();
          else collapsePhonePlayer();
        }}
      >
        {localOutputAdapter}
        {phoneExpanded && (
          <section
            className="phone-player-expanded"
            id="phone-player-panel"
            role="region"
            aria-labelledby="phone-player-heading"
          >
            <header className="phone-player-expanded-header">
              <div>
                <p className="eyebrow">{t("player.nowPlaying")}</p>
                <h2 id="phone-player-heading">{t("player.expandedHeading")}</h2>
              </div>
              <button
                ref={phoneCollapseRef}
                className="icon-button phone-player-collapse"
                type="button"
                aria-label={t("player.collapseControls")}
                title={t("player.collapseControls")}
                onClick={collapsePhonePlayer}
              >
                <PlayerIcon name="collapse" />
              </button>
            </header>

            <div className="phone-player-expanded-track">
              <strong>{loading ? t("player.loading") : player?.track?.title || t("player.noTrack")}</strong>
              <span>{player?.track?.artist || (selectedDevice ? deviceLabel(selectedDevice, t, localBrowserDevice?.id) : t("player.selectDevicePrompt"))}</span>
              {error && (
                <button className="player-error phone-player-status" type="button" onClick={() => onNotice(error, true)}>
                  {t("player.errorDetails")}
                </button>
              )}
              {!error && player?.status_warning && (
                <button
                  className="player-error phone-player-status"
                  type="button"
                  onClick={() => onStatusWarning(player.status_warning ?? null, true)}
                >
                  {t("player.statusWarningDetails")}
                </button>
              )}
            </div>

            <div className="transport phone-player-expanded-transport">
              <div className="transport-buttons">
                <button
                  className="icon-button"
                  type="button"
                  aria-label={t("player.previousTrack")}
                  disabled={!canUsePlayback || Boolean(busy)}
                  onClick={() => void command("previous")}
                >
                  <PlayerIcon name="previous" />
                </button>
                <button
                  className="icon-button"
                  type="button"
                  aria-label={t("player.nextTrack")}
                  disabled={!canUsePlayback || Boolean(busy)}
                  onClick={() => void command("next")}
                >
                  <PlayerIcon name="next" />
                </button>
              </div>
              {modeControls("phone-expanded")}
              <div className="seek-row">
                <span>{timeLabel(position, locale)}</span>
                <input
                  aria-label={t("player.seekPosition")}
                  className="seek-slider"
                  type="range"
                  min={0}
                  max={Math.max(duration, 1)}
                  step={1000}
                  value={Math.min(position, Math.max(duration, 1))}
                  disabled={!player?.track || !player.capabilities.seek || duration <= 0 || Boolean(busy)}
                  onChange={(event) => {
                    setSeeking(true);
                    seekingRef.current = true;
                    setPosition(Number(event.target.value));
                  }}
                  onPointerUp={() => void seek()}
                  onKeyUp={() => void seek()}
                />
                <span>{timeLabel(duration, locale)}</span>
              </div>
            </div>

            {outputPicker}
          </section>
        )}

        {!phoneExpanded && modeChooserOpen && (
          <section
            className="phone-player-mode-panel"
            id="phone-player-mode-panel"
            aria-labelledby="phone-player-mode-heading"
          >
            <header>
              <strong id="phone-player-mode-heading">{t("player.mode.heading")}</strong>
              <button
                className="icon-button"
                type="button"
                aria-label={t("player.mode.close")}
                title={t("player.mode.close")}
                onClick={closeModeChooser}
              >
                <PlayerIcon name="collapse" />
              </button>
            </header>
            {modeControls("phone-compact")}
          </section>
        )}

        <div className="phone-player-compact">
          <div className="now-playing phone-player-summary">
            <button
              className="player-artwork"
              type="button"
              aria-label={t("player.openQueue")}
              title={t("player.openQueue")}
              onClick={() => {
                setPhoneExpanded(false);
                setModeChooserOpen(false);
                onShowQueue();
              }}
            >
              {player?.track?.artwork_id ? (
                <img
                  src={`/api/v1/artwork/${encodeURIComponent(player.track.artwork_id)}`}
                  alt=""
                />
              ) : (
                <PlayerIcon name="music" />
              )}
            </button>
            <div className="now-playing-copy" aria-live="polite">
              <strong>{loading ? t("player.loading") : player?.track?.title || t("player.noTrack")}</strong>
              <span>{player?.track?.artist || (selectedDevice ? deviceLabel(selectedDevice, t, localBrowserDevice?.id) : t("player.selectDevicePrompt"))}</span>
              {browserRetry && <span className="browser-autoplay-compact">{t("player.browser.autoplayBlocked")}</span>}
              {localOutputRecovery.recovering && (
                <span className="local-output-recovery local-output-recovery-compact">
                  {localOutputRecovery.message
                    ? t("player.native.recoveringDetail", { message: localOutputRecovery.message })
                    : t("player.native.recovering")}
                </span>
              )}
              {error && (
                <button className="player-error" type="button" onClick={() => onNotice(error, true)}>
                  {t("player.errorDetails")}
                </button>
              )}
              {!error && player?.status_warning && (
                <button className="player-error" type="button" onClick={() => onStatusWarning(player.status_warning ?? null, true)}>
                  {t("player.statusWarningDetails")}
                </button>
              )}
            </div>
          </div>

          <div className="phone-player-primary-actions">
            <button
              ref={modeChooserButtonRef}
              className="icon-button phone-player-mode-trigger"
              type="button"
              aria-label={compactModeLabel}
              title={compactModeLabel}
              aria-controls="phone-player-mode-panel"
              aria-expanded={!phoneExpanded && modeChooserOpen}
              onClick={() => setModeChooserOpen((current) => !current)}
            >
              <PlayerIcon name="modes" />
            </button>
            <button
              className="icon-button player-primary-control"
              type="button"
              aria-label={browserRetry ? t("player.browser.allowPlayback") : canPause ? t("player.pause") : t("player.play")}
              disabled={!browserRetry && (!(canPause ? canControl : canUsePlayback) || Boolean(busy))}
              onClick={browserRetry ? () => localOutputRef.current?.retryPlayback?.() : () => void command(canPause ? "pause" : "play")}
            >
              <PlayerIcon name={canPause ? "pause" : "play"} />
            </button>
            <button
              className="icon-button"
              type="button"
              aria-label={t("player.stop")}
              disabled={!canControl || player?.state === "stopped" || Boolean(busy)}
              onClick={() => void command("stop")}
            >
              <PlayerIcon name="stop" />
            </button>
            <button
              ref={phoneExpandRef}
              className="icon-button"
              type="button"
              aria-label={phoneExpanded ? t("player.collapseControls") : t("player.expandControls")}
              title={phoneExpanded ? t("player.collapseControls") : t("player.expandControls")}
              aria-controls="phone-player-panel"
              aria-expanded={phoneExpanded}
              onClick={phoneExpanded ? collapsePhonePlayer : () => {
                setModeChooserOpen(false);
                setPhoneExpanded(true);
              }}
            >
              <PlayerIcon name={phoneExpanded ? "collapse" : "expand"} />
            </button>
          </div>
        </div>
      </footer>
    );
  }

  return (
    <footer className="player-bar" aria-label={t("player.nowPlaying")}>
      {localOutputAdapter}
      <div className="now-playing">
        <button
          className="player-artwork"
          type="button"
          aria-label={t("player.openQueue")}
          title={t("player.openQueue")}
          onClick={onShowQueue}
        >
          {player?.track?.artwork_id ? (
            <img
              src={`/api/v1/artwork/${encodeURIComponent(player.track.artwork_id)}`}
              alt=""
            />
          ) : (
            <PlayerIcon name="music" />
          )}
        </button>
        <div className="now-playing-copy" aria-live="polite">
          <strong>{loading ? t("player.loading") : player?.track?.title || t("player.noTrack")}</strong>
          <span>{player?.track?.artist || (selectedDevice ? deviceLabel(selectedDevice, t, localBrowserDevice?.id) : t("player.selectDevicePrompt"))}</span>
          {error && <button className="player-error" type="button" onClick={() => onNotice(error, true)}>{t("player.errorDetails")}</button>}
          {!error && player?.status_warning && (
            <button className="player-error" type="button" onClick={() => onStatusWarning(player.status_warning ?? null, true)}>
              {t("player.statusWarningDetails")}
            </button>
          )}
        </div>
      </div>

      <div className="transport">
        <div className="transport-buttons">
          {shuffleButton()}
          <button
            className="icon-button"
            type="button"
            aria-label={t("player.previousTrack")}
            disabled={!canUsePlayback || Boolean(busy)}
            onClick={() => void command("previous")}
          >
            <PlayerIcon name="previous" />
          </button>
          <button
            className="icon-button player-primary-control"
            type="button"
            aria-label={browserRetry ? t("player.browser.allowPlayback") : canPause ? t("player.pause") : t("player.play")}
            disabled={!browserRetry && (!(canPause ? canControl : canUsePlayback) || Boolean(busy))}
            onClick={browserRetry ? () => localOutputRef.current?.retryPlayback?.() : () => void command(canPause ? "pause" : "play")}
          >
            <PlayerIcon name={canPause ? "pause" : "play"} />
          </button>
          <button
            className="icon-button"
            type="button"
            aria-label={t("player.stop")}
            disabled={!canControl || player?.state === "stopped" || Boolean(busy)}
            onClick={() => void command("stop")}
          >
            <PlayerIcon name="stop" />
          </button>
          <button
            className="icon-button"
            type="button"
            aria-label={t("player.nextTrack")}
            disabled={!canUsePlayback || Boolean(busy)}
            onClick={() => void command("next")}
          >
            <PlayerIcon name="next" />
          </button>
          {repeatButton()}
        </div>
        <div className="seek-row">
          <span>{timeLabel(position, locale)}</span>
          <input
            aria-label={t("player.seekPosition")}
            className="seek-slider"
            type="range"
            min={0}
            max={Math.max(duration, 1)}
            step={1000}
            value={Math.min(position, Math.max(duration, 1))}
            disabled={!player?.track || !player.capabilities.seek || duration <= 0 || Boolean(busy)}
            onChange={(event) => {
              setSeeking(true);
              seekingRef.current = true;
              setPosition(Number(event.target.value));
            }}
            onPointerUp={() => void seek()}
            onKeyUp={() => void seek()}
          />
          <span>{timeLabel(duration, locale)}</span>
        </div>
      </div>

      {outputPicker}
    </footer>
  );
}
