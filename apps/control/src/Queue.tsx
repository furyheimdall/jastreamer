import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import { useI18n } from "./i18n";
import TrackInfoDialog from "./TrackInfoDialog";
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

function messageFrom(error: unknown, fallback: string): string {
  return error instanceof ApiError ? error.message : fallback;
}

function QueueIcon({ name }: { name: "up" | "down" | "remove" | "play" | "info" | "music" }) {
  const paths = {
    up: <path d="m18 15-6-6-6 6" />,
    down: <path d="m6 9 6 6 6-6" />,
    remove: <path d="M3 6h18M8 6V4h8v2m-9 0 1 14h8l1-14M10 11v5m4-5v5" />,
    play: <path d="m8 5 11 7-11 7Z" />,
    info: <><circle cx="12" cy="12" r="9" /><path d="M12 11v6M12 7h.01" /></>,
    music: <><path d="M9 18V5l11-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="17" cy="16" r="3" /></>,
  };
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      {paths[name]}
    </svg>
  );
}

export default function Queue({ revision, onNotice, onQueueChange }: QueueProps) {
  const { locale, t } = useI18n();
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale]);
  const [queue, setQueue] = useState<QueueState | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busyEntry, setBusyEntry] = useState("");
  const [saving, setSaving] = useState(false);
  const [playlistName, setPlaylistName] = useState("");
  const [infoTrackID, setInfoTrackID] = useState<string | null>(null);

  const loadQueue = useCallback(async () => {
    try {
      const next = await api<QueueState>("/queue");
      setQueue(next);
      setError("");
    } catch (requestError) {
      setError(messageFrom(requestError, t("common.requestFailed")));
    } finally {
      setLoading(false);
    }
  }, [t]);

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
          ? t("queue.conflictReloaded")
          : messageFrom(requestError, t("common.requestFailed")),
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
      onNotice(t("queue.playRequested", { title: entry.track.title || t("queue.unknownTitle") }));
      onQueueChange();
    } catch (requestError) {
      onNotice(messageFrom(requestError, t("common.requestFailed")), true);
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
      onNotice(t("queue.saved"));
    } catch (requestError) {
      onNotice(messageFrom(requestError, t("common.requestFailed")), true);
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="content-section queue-page" aria-labelledby="queue-heading">
      <header className="page-heading queue-heading-row">
        <div>
          <p className="eyebrow">{t("queue.eyebrow")}</p>
          <h1 id="queue-heading">{t("queue.heading")}</h1>
          <p className="muted">{t("queue.description")}</p>
        </div>
        <button
          className="button button-ghost"
          type="button"
          disabled={!queue?.entries.length || Boolean(busyEntry)}
          onClick={() => void mutate("", "clear")}
        >
          {t("queue.clearUpcoming")}
        </button>
      </header>

      {error && (
        <div className="inline-error" role="alert">
          <span>{error}</span>
          <button className="button button-ghost" type="button" onClick={() => void loadQueue()}>
            {t("common.retry")}
          </button>
        </div>
      )}

      {loading && !queue ? (
        <div className="loading-block" aria-live="polite">{t("queue.loading")}</div>
      ) : !queue?.entries.length ? (
        <div className="empty-state">
          <h2>{t("queue.emptyHeading")}</h2>
          <p>{t("queue.emptyDescription")}</p>
        </div>
      ) : (
        <ol className="queue-list">
          {queue.entries.map((entry, index) => {
            const locked = entry.status === "playing";
            const busy = busyEntry === entry.id;
            const trackTitle = entry.track.title || t("queue.unknownTitle");
            const position = numberFormatter.format(index + 1);
            return (
              <li className={`queue-row ${locked ? "is-current" : ""}`} key={entry.id}>
                <span className="queue-index" aria-label={t("queue.position", { number: position })}>{position}</span>
                <button
                  className="queue-artwork"
                  type="button"
                  aria-label={t("queue.viewTrackInfo", { title: trackTitle })}
                  onClick={() => setInfoTrackID(entry.track.id)}
                >
                  {entry.track.artwork_id ? (
                    <img
                      src={`/api/v1/artwork/${encodeURIComponent(entry.track.artwork_id)}`}
                      alt=""
                      loading="lazy"
                    />
                  ) : (
                    <QueueIcon name="music" />
                  )}
                  <span className="queue-artwork-info" aria-hidden="true"><QueueIcon name="info" /></span>
                </button>
                <div className="queue-track-copy">
                  <button className="queue-track-title" type="button" onClick={() => setInfoTrackID(entry.track.id)} aria-label={t("queue.viewTrackInfo", { title: trackTitle })}><strong>{trackTitle}</strong></button>
                  <span>{entry.track.artist || t("queue.unknownArtist")}</span>
                  {!entry.track.available && <em>{t("queue.fileUnavailable")}</em>}
                </div>
                <span className="queue-duration">{durationLabel(entry.track.duration_ms)}</span>
                <div className="queue-actions" aria-label={t("queue.trackActions", { title: trackTitle })}>
                  <button
                    className="icon-button queue-play-button"
                    type="button"
                    aria-label={t("queue.playTrack", { title: trackTitle })}
                    disabled={!entry.track.available || Boolean(busyEntry)}
                    onClick={() => void playEntry(entry)}
                  >
                    <QueueIcon name="play" />
                  </button>
                  <button
                    className="icon-button"
                    type="button"
                    aria-label={t("queue.moveUp")}
                    disabled={index === 0 || locked || busy || Boolean(busyEntry)}
                    onClick={() => void mutate(entry.id, "move", index - 1)}
                  >
                    <QueueIcon name="up" />
                  </button>
                  <button
                    className="icon-button"
                    type="button"
                    aria-label={t("queue.moveDown")}
                    disabled={index === queue.entries.length - 1 || locked || busy || Boolean(busyEntry)}
                    onClick={() => void mutate(entry.id, "move", index + 1)}
                  >
                    <QueueIcon name="down" />
                  </button>
                  <button
                    className="icon-button danger-button"
                    type="button"
                    aria-label={t("queue.remove")}
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
          <h2>{t("queue.saveHeading")}</h2>
          <p className="muted">{t("queue.saveDescription")}</p>
        </div>
        <label className="sr-only" htmlFor="queue-playlist-name">{t("queue.playlistName")}</label>
        <input
          className="input"
          id="queue-playlist-name"
          value={playlistName}
          maxLength={120}
          placeholder={t("queue.playlistName")}
          onChange={(event) => setPlaylistName(event.target.value)}
        />
        <button
          className="button button-primary"
          type="submit"
          disabled={saving || !playlistName.trim() || !queue?.entries.length}
        >
          {t(saving ? "common.saving" : "common.save")}
        </button>
      </form>
      <TrackInfoDialog trackId={infoTrackID} onClose={() => setInfoTrackID(null)} />
    </section>
  );
}
