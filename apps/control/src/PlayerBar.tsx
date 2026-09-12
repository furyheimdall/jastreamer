import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import TrackInfoDialog from "./TrackInfoDialog";
import type { Device, PairingRequest, PairingStatus, PlayerState } from "./types";

interface PlayerBarProps {
  revision: number;
  onNotice: (message: string, error?: boolean) => void;
  onQueueChange: () => void;
}

function timeLabel(milliseconds: number): string {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

function errorMessage(error: unknown): string {
  return error instanceof ApiError ? error.message : "요청을 완료하지 못했습니다.";
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

export default function PlayerBar({ revision, onNotice, onQueueChange }: PlayerBarProps) {
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
  const [infoTrackId, setInfoTrackId] = useState<string | null>(null);
  const closeTrackInfo = useCallback(() => setInfoTrackId(null), []);
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
      if (!seekingRef.current) setPosition(nextPlayer.position_ms);
      if (nextPlayer.error) messages.push(nextPlayer.error);
    } else {
      messages.push(errorMessage(playerResult.reason));
    }
    if (rendererResult.status === "fulfilled") {
      setDevices(rendererResult.value.items ?? []);
    } else {
      const message = errorMessage(rendererResult.reason);
      if (!messages.includes(message)) messages.push(message);
    }
    applyError(messages.join("\n\n"), playerResult.status === "fulfilled" ? playerResult.value.revision : -1);
    setLoading(false);
  }, [applyError]);

  useEffect(() => {
    void load();
  }, [load, revision]);

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
    ? `연결되지 않은 기기 [${player.renderer_id.startsWith("airplay:") ? "AirPlay" : "UPnP"}]`
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
      setPosition(next.position_ms);
      applyError(next.error || "", next.revision);
      onQueueChange();
    } catch (requestError) {
      const message = errorMessage(requestError);
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
      setPosition(next.position_ms);
      applyError(next.error || "", next.revision);
    } catch (requestError) {
      setPosition(player.position_ms);
      const message = errorMessage(requestError);
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
      applyError(next.error || "", next.revision);
      onNotice("재생 기기를 변경했습니다.");
    } catch (requestError) {
      const message = errorMessage(requestError);
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
    setPairingStatus({ required: true, prompt: "페어링 정보를 확인하는 중…" });
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
        onNotice(`${deviceLabel(selectedDevice)} 페어링을 완료했습니다.`);
      }
      return next;
    } catch (requestError) {
      const message = errorMessage(requestError);
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
      onNotice("기기를 다시 찾고 있습니다.");
      window.setTimeout(() => void load(), 1200);
    } catch (requestError) {
      onNotice(errorMessage(requestError), true);
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
    <footer className="player-bar" aria-label="현재 재생">
      <div className="now-playing">
        <button
          className="player-artwork"
          type="button"
          disabled={!player?.track}
          aria-label={player?.track ? `${player.track.title} 곡 정보` : "곡 정보"}
          title="곡 정보"
          onClick={() => { if (player?.track) setInfoTrackId(player.track.id); }}
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
          <strong>{loading ? "재생 정보를 불러오는 중…" : player?.track?.title || "재생 중인 곡 없음"}</strong>
          <span>{player?.track?.artist || (selectedDevice ? deviceLabel(selectedDevice) : "기기를 선택해 주세요")}</span>
          {error && <button className="player-error" type="button" onClick={() => onNotice(error, true)}>오류 상세 보기</button>}
        </div>
      </div>

      <div className="transport">
        <div className="transport-buttons">
          <button
            className="icon-button"
            type="button"
            aria-label="이전 곡"
            disabled={!canUsePlayback || Boolean(busy)}
            onClick={() => void command("previous")}
          >
            <PlayerIcon name="previous" />
          </button>
          <button
            className="icon-button player-primary-control"
            type="button"
            aria-label={canPause ? "일시 정지" : "재생"}
            disabled={!(canPause ? canControl : canUsePlayback) || Boolean(busy)}
            onClick={() => void command(canPause ? "pause" : "play")}
          >
            <PlayerIcon name={canPause ? "pause" : "play"} />
          </button>
          <button
            className="icon-button"
            type="button"
            aria-label="정지"
            disabled={!canControl || player?.state === "stopped" || Boolean(busy)}
            onClick={() => void command("stop")}
          >
            <PlayerIcon name="stop" />
          </button>
          <button
            className="icon-button"
            type="button"
            aria-label="다음 곡"
            disabled={!canUsePlayback || Boolean(busy)}
            onClick={() => void command("next")}
          >
            <PlayerIcon name="next" />
          </button>
        </div>
        <div className="seek-row">
          <span>{timeLabel(position)}</span>
          <input
            aria-label="재생 위치"
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
          <span>{timeLabel(duration)}</span>
        </div>
      </div>

      <div className="output-picker">
        <label htmlFor="player-output">재생 기기</label>
        <div className="output-control">
          <select
            id="player-output"
            value={player?.renderer_id || ""}
            disabled={player?.state !== "stopped" || Boolean(player?.pending_command) || Boolean(busy)}
            onChange={(event) => void selectOutput(event.target.value)}
          >
            <option value="">기기를 선택하세요</option>
            {missingSelectedDeviceLabel && player?.renderer_id && (
              <option value={player.renderer_id} disabled>{missingSelectedDeviceLabel}</option>
            )}
            {devices.map((device) => (
              <option key={device.id} value={device.id} disabled={!device.online}>
                {deviceLabel(device)}{device.online ? "" : " (오프라인)"}
              </option>
            ))}
          </select>
          <button
            className="icon-button"
            type="button"
            aria-label="재생 기기 다시 찾기"
            disabled={Boolean(busy)}
            onClick={() => void refreshDevices()}
          >
            <PlayerIcon name="refresh" />
          </button>
        </div>
        {pairingRequired && selectedDevice && (
          <section className="pairing-panel" aria-labelledby="pairing-heading" aria-busy={busy === "pairing"}>
            <h2 id="pairing-heading">{deviceLabel(selectedDevice)} 페어링</h2>
            <p className="pairing-prompt" aria-live="polite">
              {pairingStatus?.prompt || (
                selectedDevice.pairing_required && !pairingStarted
                  ? "페어링을 시작하면 AirPlay 기기에 새 PIN이 표시됩니다."
                  : selectedDevice.pairing_required && selectedDevice.password_required
                    ? "기기에 표시된 PIN과 AirPlay 암호를 입력하세요."
                    : selectedDevice.pairing_required
                      ? "기기에 표시된 PIN을 입력하세요."
                      : "AirPlay 암호를 입력하세요."
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
                    {busy === "pairing" ? "시작 중…" : "페어링 시작"}
                  </button>
                </div>
              ) : (
                <form className="pairing-form" autoComplete="off" onSubmit={(event) => void pairDevice(event)}>
                  {selectedDevice.pairing_required && (
                    <label>
                      <span className="field-label">PIN</span>
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
                      <span className="field-label">AirPlay 암호</span>
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
                    {busy === "pairing" ? "페어링 중…" : "페어링"}
                  </button>
                </form>
              )
            ) : (
              <p className="pairing-stopped-message">재생을 정지하고 진행 중인 작업이 끝난 뒤 페어링하세요.</p>
            )}
            {pairingError && <p className="error-text pairing-error" role="alert">{pairingError}</p>}
          </section>
        )}
        {playing && <span className="playing-indicator">재생 중</span>}
      </div>
      <TrackInfoDialog trackId={infoTrackId} onClose={closeTrackInfo} />
    </footer>
  );
}
