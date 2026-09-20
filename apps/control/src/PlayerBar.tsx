import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { api } from "./api";
import LocalOutput, { hasNativeAndroidAudio, type LocalOutputHandle } from "./LocalOutput";
import { useI18n, type MessageKey } from "./i18n";
import { isPhone, localOutputName } from "./device";
import type { Device, PairingRequest, PairingStatus, PlayerState, StatusWarning } from "./types";

interface PlayerBarProps {
  revision: number;
  phoneExpanded: boolean;
  onPhoneExpandedChange: (expanded: boolean) => void;
  onNotice: (message: string, error?: boolean) => void;
  onStatusWarning: (warning: StatusWarning | null, reopen?: boolean) => void;
  onQueueChange: () => void;
  onShowQueue: () => void;
}

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

function PlayerIcon({ name }: { name: "play" | "pause" | "stop" | "next" | "previous" | "music" | "refresh" | "expand" | "collapse" | "edit" }) {
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
  };
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">{paths[name]}</svg>;
}

export default function PlayerBar({ revision, phoneExpanded, onPhoneExpandedChange: setPhoneExpanded, onNotice, onStatusWarning, onQueueChange, onShowQueue }: PlayerBarProps) {
  const { locale, t } = useI18n();
  const [player, setPlayer] = useState<PlayerState | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
  const [localBrowserDevice, setLocalBrowserDevice] = useState<Device | null>(null);
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
  const observedPlayerError = useRef<{ revision: number; message: string } | null>(null);
  const localOutputRef = useRef<LocalOutputHandle>(null);
  const [usesNativeOutput] = useState(hasNativeAndroidAudio);
  const [defaultBrowserName] = useState(() => localOutputName(usesNativeOutput));
  const [browserAlias, setBrowserAlias] = useState("");
  const [nameDraft, setNameDraft] = useState("");
  const [nameStorageKey, setNameStorageKey] = useState<string | null>(null);
  const [nameEditorOpen, setNameEditorOpen] = useState(false);
  const [nameSaving, setNameSaving] = useState(false);
  const [nameError, setNameError] = useState("");
  const [localOutputRecovery, setLocalOutputRecovery] = useState({ recovering: false, message: "" });
  const browserName = browserAlias || defaultBrowserName;

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

  const applyError = useCallback((message: string, playerRevision: number) => {
    setError(message);
    if (!message) {
      observedPlayerError.current = null;
      return;
    }
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
    } else {
      const message = errorMessage(rendererResult.reason, t("common.requestFailed"));
      if (!messages.includes(message)) messages.push(message);
    }
    applyError(messages.join("\n\n"), playerResult.status === "fulfilled" ? playerResult.value.revision : -1);
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
      await api<void>("/renderers/refresh", {
        method: "POST",
        body: JSON.stringify({}),
      });
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
  const localOutputAdapter = (
    <LocalOutput
      ref={localOutputRef}
      name={browserName}
      disconnectedError={t("player.browser.disconnected")}
      registrationError={t("player.browser.registrationFailed")}
      actionError={t("player.browser.actionFailed")}
      bridgeError={t("player.native.bridgeFailed")}
      onDeviceChange={setLocalBrowserDevice}
      onAutoplayBlocked={setBrowserAutoplayBlocked}
      onRecoveryChange={handleLocalOutputRecovery}
      onError={handleLocalOutputError}
    />
  );
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
            <option value="browser:local" disabled={!nameStorageKey}>{browserName} ({t("player.browser.thisDevice")}) [{t("player.protocol.browser")}]</option>
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
          onClick={() => setNameEditorOpen((current) => !current)}
        >
          <PlayerIcon name="edit" />
        </button>
      </div>
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
      {pairingRequired && selectedDevice && !nameEditorOpen && (
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

  function collapsePhonePlayer() {
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
          event.preventDefault();
          event.stopPropagation();
          collapsePhonePlayer();
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

        <div className="phone-player-compact">
          <div className="now-playing phone-player-summary">
            <button
              className="player-artwork"
              type="button"
              aria-label={t("player.openQueue")}
              title={t("player.openQueue")}
              onClick={() => {
                setPhoneExpanded(false);
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
              onClick={phoneExpanded ? collapsePhonePlayer : () => setPhoneExpanded(true)}
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
