import { useCallback, useEffect, useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import type { Playlist, PlayerState, QueueEntry, QueueState } from "./types";

interface QueueProps {
  revision: number;
  onNotice: (message: string, error?: boolean) => void;
  onQueueChange: () => void;
}

function durationLabel(milliseconds: number): string {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

function messageFrom(error: unknown): string {
  return error instanceof ApiError ? error.message : "요청을 완료하지 못했습니다.";
}

function QueueIcon({ name }: { name: "up" | "down" | "remove" | "play" }) {
  const path = {
    up: "m18 15-6-6-6 6",
    down: "m6 9 6 6 6-6",
    remove: "M3 6h18M8 6V4h8v2m-9 0 1 14h8l1-14M10 11v5m4-5v5",
    play: "m8 5 11 7-11 7Z",
  }[name];
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d={path} />
    </svg>
  );
}

export default function Queue({ revision, onNotice, onQueueChange }: QueueProps) {
  const [queue, setQueue] = useState<QueueState | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busyEntry, setBusyEntry] = useState("");
  const [saving, setSaving] = useState(false);
  const [playlistName, setPlaylistName] = useState("");

  const loadQueue = useCallback(async () => {
    try {
      const next = await api<QueueState>("/queue");
      setQueue(next);
      setError("");
    } catch (requestError) {
      setError(messageFrom(requestError));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadQueue();
  }, [loadQueue, revision]);

  async function mutate(entryID: string, action: string, index?: number) {
    if (!queue) return;
    setBusyEntry(entryID || action);
    try {
      const next = await api<QueueState>("/queue", {
        method: "POST",
        body: JSON.stringify({
          action,
          entry_id: entryID || undefined,
          index,
          revision: queue.revision,
        }),
      });
      setQueue(next);
      setError("");
      onQueueChange();
    } catch (requestError) {
      const conflict = requestError instanceof ApiError && requestError.status === 409;
      onNotice(
        conflict
          ? "대기열이 다른 곳에서 변경되었습니다. 최신 내용을 불러왔습니다."
          : messageFrom(requestError),
        true,
      );
      if (conflict) await loadQueue();
    } finally {
      setBusyEntry("");
    }
  }

  async function playEntry(entry: QueueEntry) {
    setBusyEntry(entry.id);
    try {
      await api<PlayerState>("/player", {
        method: "POST",
        body: JSON.stringify({ action: "play", entry_id: entry.id }),
      });
      onNotice(`‘${entry.track.title}’ 재생을 요청했습니다.`);
      onQueueChange();
    } catch (requestError) {
      onNotice(messageFrom(requestError), true);
    } finally {
      setBusyEntry("");
    }
  }

  async function savePlaylist(event: FormEvent) {
    event.preventDefault();
    const name = playlistName.trim();
    if (!queue || !name || queue.entries.length === 0) return;
    setSaving(true);
    try {
      await api<Playlist>("/playlists", {
        method: "POST",
        body: JSON.stringify({
          name,
          track_ids: queue.entries.map((entry) => entry.track_id),
        }),
      });
      setPlaylistName("");
      onNotice("현재 대기열을 플레이리스트로 저장했습니다.");
    } catch (requestError) {
      onNotice(messageFrom(requestError), true);
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="content-section queue-page" aria-labelledby="queue-heading">
      <header className="page-heading queue-heading-row">
        <div>
          <p className="eyebrow">재생 순서</p>
          <h1 id="queue-heading">대기열</h1>
          <p className="muted">순서와 중복을 그대로 유지하며 서버에 저장됩니다.</p>
        </div>
        <button
          className="button button-ghost"
          type="button"
          disabled={!queue?.entries.length || Boolean(busyEntry)}
          onClick={() => void mutate("", "clear")}
        >
          다음 곡 모두 지우기
        </button>
      </header>

      {error && (
        <div className="inline-error" role="alert">
          <span>{error}</span>
          <button className="button button-ghost" type="button" onClick={() => void loadQueue()}>
            다시 시도
          </button>
        </div>
      )}

      {loading && !queue ? (
        <div className="loading-block" aria-live="polite">대기열을 불러오는 중…</div>
      ) : !queue?.entries.length ? (
        <div className="empty-state">
          <h2>대기열이 비어 있습니다</h2>
          <p>보관함이나 플레이리스트에서 곡을 추가해 주세요.</p>
        </div>
      ) : (
        <ol className="queue-list">
          {queue.entries.map((entry, index) => {
            const locked = entry.status === "playing";
            const busy = busyEntry === entry.id;
            return (
              <li className={`queue-row ${locked ? "is-current" : ""}`} key={entry.id}>
                <span className="queue-index" aria-label={`${index + 1}번`}>{index + 1}</span>
                <button
                  className="queue-artwork"
                  type="button"
                  aria-label={`${entry.track.title} 재생`}
                  disabled={!entry.track.available || busy}
                  onClick={() => void playEntry(entry)}
                >
                  {entry.track.artwork_id ? (
                    <img
                      src={`/api/v1/artwork/${encodeURIComponent(entry.track.artwork_id)}`}
                      alt=""
                      loading="lazy"
                    />
                  ) : (
                    <QueueIcon name="play" />
                  )}
                </button>
                <div className="queue-track-copy">
                  <strong>{entry.track.title || "제목 없음"}</strong>
                  <span>{entry.track.artist || "아티스트 정보 없음"}</span>
                  {!entry.track.available && <em>파일을 사용할 수 없음</em>}
                </div>
                <span className="queue-duration">{durationLabel(entry.track.duration_ms)}</span>
                <div className="queue-actions" aria-label={`${entry.track.title} 대기열 작업`}>
                  <button
                    className="icon-button"
                    type="button"
                    aria-label="한 칸 위로"
                    disabled={index === 0 || locked || busy || Boolean(busyEntry)}
                    onClick={() => void mutate(entry.id, "move", index - 1)}
                  >
                    <QueueIcon name="up" />
                  </button>
                  <button
                    className="icon-button"
                    type="button"
                    aria-label="한 칸 아래로"
                    disabled={index === queue.entries.length - 1 || locked || busy || Boolean(busyEntry)}
                    onClick={() => void mutate(entry.id, "move", index + 1)}
                  >
                    <QueueIcon name="down" />
                  </button>
                  <button
                    className="icon-button danger-button"
                    type="button"
                    aria-label="대기열에서 삭제"
                    disabled={locked || busy || Boolean(busyEntry)}
                    onClick={() => void mutate(entry.id, "remove")}
                  >
                    <QueueIcon name="remove" />
                  </button>
                </div>
              </li>
            );
          })}
        </ol>
      )}

      <form className="save-queue" onSubmit={(event) => void savePlaylist(event)}>
        <div>
          <h2>플레이리스트로 저장</h2>
          <p className="muted">현재 순서와 중복 곡을 그대로 보관합니다.</p>
        </div>
        <label className="sr-only" htmlFor="queue-playlist-name">플레이리스트 이름</label>
        <input
          className="input"
          id="queue-playlist-name"
          value={playlistName}
          maxLength={120}
          placeholder="플레이리스트 이름"
          onChange={(event) => setPlaylistName(event.target.value)}
        />
        <button
          className="button button-primary"
          type="submit"
          disabled={saving || !playlistName.trim() || !queue?.entries.length}
        >
          {saving ? "저장 중…" : "저장"}
        </button>
      </form>
    </section>
  );
}
