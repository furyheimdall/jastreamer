import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import { useI18n } from "./i18n";
import type { Device, PairingRequest, PairingStatus, PlayerState, StatusWarning } from "./types";

interface PlayerBarProps {
  revision: number;
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
  return error instanceof ApiError ? error.message : fallback;
}

function deviceLabel(device: Device): string {
  const protocol = device.protocol === "airplay" ? "AirPlay" : "UPnP";
  return `${device.name} [${protocol}]`;
}

function PlayerIcon({ name }: { name: "play" | "pause" | "stop" | "next" | "previous" | "music" | "refresh" }) {
  const paths = {
    play: <path d="m8 5 11 7-11 7Z" />,
    pause: <path d="M9 5v14M15 5v14" />,
    stop: <path d="M7 7h10v10H7z" />,
    next: <><path d="m6 6 8 6-8 6Z" /><path d="M18 6v12" /></>,
    previous: <><path d="m18 6-8 6 8 6Z" /><path d="M6 6v12" /></>,
    music: <><path d="M9 18V5l10-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="16" cy="16" r="3" /></>,
    refresh: <><path d="M20 7h-5V2" /><path d="M20 7a8 8 0 1 0 1 7" /></>,
  };
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">{paths[name]}</svg>;
}

export default function PlayerBar({ revision, onNotice, onStatusWarning, onQueueChange, onShowQueue }: PlayerBarProps) {
  const { locale, t } = useI18n();
  const [player, setPlayer] = useState<PlayerState | null>(null);
  const [devices, setDevices] = useState<Device[]>([]);
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
  const observedPlayerError = useRef<{ revision: number; message: string } | null>(null);

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
    if (!player || player.state !== "playing" || seeking) return;
    const timer = window.setInterval(() => {
      setPosition((current) => Math.min(player.duration_ms || current + 1000, current + 1000));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [player, seeking]);

  const selectedDevice = useMemo(
    () => devices.find((device) => device.id === player?.renderer_id),
    [devices, player?.renderer_id],
  );
  const missingSelectedDeviceLabel = player?.renderer_id && !selectedDevice
    ? t("player.disconnectedOutput", {
        protocol: player.renderer_id.startsWith("airplay:") ? "AirPlay" : "UPnP",
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
        onNotice(t("player.pairing.complete", { device: deviceLabel(selectedDevice) }));
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
  const playing = player?.state === "playing" || player?.state === "starting";
  const duration = player?.duration_ms || player?.track?.duration_ms || 0;

  return (
    <footer className="player-bar" aria-label={t("player.nowPlaying")}>
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
          <span>{player?.track?.artist || (selectedDevice ? deviceLabel(selectedDevice) : t("player.selectDevicePrompt"))}</span>
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
            aria-label={canPause ? t("player.pause") : t("player.play")}
            disabled={!(canPause ? canControl : canUsePlayback) || Boolean(busy)}
            onClick={() => void command(canPause ? "pause" : "play")}
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

      <div className="output-picker">
        <label htmlFor="player-output">{t("player.output")}</label>
        <div className="output-control">
          <select
            id="player-output"
            value={player?.renderer_id || ""}
            disabled={player?.state !== "stopped" || Boolean(player?.pending_command) || Boolean(busy)}
            onChange={(event) => void selectOutput(event.target.value)}
          >
            <option value="">{t("player.selectOutput")}</option>
            {missingSelectedDeviceLabel && player?.renderer_id && (
              <option value={player.renderer_id} disabled>{missingSelectedDeviceLabel}</option>
            )}
            {devices.map((device) => (
              <option key={device.id} value={device.id} disabled={!device.online}>
                {deviceLabel(device)}{device.online ? "" : ` (${t("player.offline")})`}
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
        </div>
        {pairingRequired && selectedDevice && (
          <section className="pairing-panel" aria-labelledby="pairing-heading" aria-busy={busy === "pairing"}>
            <h2 id="pairing-heading">{t("player.pairing.heading", { device: deviceLabel(selectedDevice) })}</h2>
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
      </div>
    </footer>
  );
}
