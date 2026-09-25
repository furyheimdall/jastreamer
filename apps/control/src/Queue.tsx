import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import { useI18n } from "./i18n";
import TrackInfoDialog from "./TrackInfoDialog";
import type { Playlist, PlayerState, QueueEntry, QueueState, Track } from "./types";
import type { JastreamerDownloads } from "./JastreamerDownloads";

interface QueueProps {
  revision: number;
  onNotice: (message: string, error?: boolean) => void;
  onQueueChange: () => void;
  downloads: JastreamerDownloads;
}

function durationLabel(milliseconds: number): string {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

function messageFrom(error: unknown, fallback: string): string {
  return error instanceof ApiError ? error.message : fallback;
}

function QueueIcon({ name }: { name: "up" | "down" | "remove" | "play" | "info" | "music" | "heart" }) {
  const paths = {
    up: <path d="m18 15-6-6-6 6" />,
    down: <path d="m6 9 6 6 6-6" />,
    remove: <path d="M3 6h18M8 6V4h8v2m-9 0 1 14h8l1-14M10 11v5m4-5v5" />,
    play: <path d="m8 5 11 7-11 7Z" />,
    info: <><circle cx="12" cy="12" r="9" /><path d="M12 11v6M12 7h.01" /></>,
    music: <><path d="M9 18V5l11-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="17" cy="16" r="3" /></>,
    heart: <path d="M20.8 5.8a5.5 5.5 0 0 0-7.8 0L12 6.9l-1.1-1.1a5.5 5.5 0 0 0-7.8 7.8L12 22l8.8-8.4a5.5 5.5 0 0 0 0-7.8z" />,
  };
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      {paths[name]}
    </svg>
  );
}

export default function Queue({ revision, onNotice, onQueueChange, downloads }: QueueProps) {
  const { locale, t } = useI18n();
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale]);
  const [queue, setQueue] = useState<QueueState | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busyEntry, setBusyEntry] = useState("");
  const [saving, setSaving] = useState(false);
  const [clearRevision, setClearRevision] = useState<number | null>(null);
  const [playlistName, setPlaylistName] = useState("");
  const [infoTrackID, setInfoTrackID] = useState<string | null>(null);
  const [likeBusy, setLikeBusy] = useState<Set<string>>(() => new Set());
  const clearArmed = clearRevision !== null && clearRevision === queue?.revision;

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
      if (action === "clear") setClearRevision(null);
      onQueueChange();
    } catch (requestError) {
      const conflict = requestError instanceof ApiError && requestError.code === "REVISION_CONFLICT";
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

  async function toggleLiked(track: Track) {
    if (likeBusy.has(track.id)) return;
    setLikeBusy((current) => new Set(current).add(track.id));
    try {
      const updated = await api<Track>(`/library/tracks/${encodeURIComponent(track.id)}/like`, {
        method: "PUT",
        body: JSON.stringify({ liked: !track.liked }),
      });
      setQueue((current) => current ? {
        ...current,
        entries: current.entries.map((entry) => entry.track_id === updated.id ? { ...entry, track: updated } : entry),
      } : current);
      onNotice(t(updated.liked ? "library.likedTrack" : "library.unlikedTrack", { title: updated.title }));
    } catch (requestError) {
      onNotice(messageFrom(requestError, t("common.requestFailed")), true);
    } finally {
      setLikeBusy((current) => {
        const next = new Set(current);
        next.delete(track.id);
        return next;
      });
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
        <div className="queue-heading-copy">
          <p className="eyebrow">{t("queue.eyebrow")}</p>
          <h1 id="queue-heading">{t("queue.heading")}</h1>
          <p className="muted">{t("queue.description")}</p>
        </div>
        <div
          className="queue-global-management"
          role="group"
          aria-label={t("queue.actions")}
        >
          {!clearArmed ? (
            <button
              className="button button-ghost danger-button"
              type="button"
              disabled={!queue?.entries.length || Boolean(busyEntry)}
              onClick={() => setClearRevision(queue?.revision ?? null)}
            >
              {t("queue.clear")}
            </button>
          ) : (
            <div className="queue-clear-confirmation">
              <strong>{t("queue.clearConfirm")}</strong>
              <div className="queue-clear-confirmation-actions">
                <button
                  className="button button-ghost"
                  type="button"
                  disabled={Boolean(busyEntry)}
                  onClick={() => setClearRevision(null)}
                >
                  {t("common.cancel")}
                </button>
                <button
                  className="button button-ghost danger-button"
                  type="button"
                  disabled={!queue?.entries.length || Boolean(busyEntry)}
                  onClick={() => void mutate("", "clear")}
                >
                  {t("queue.clear")}
                </button>
              </div>
            </div>
          )}
        </div>
      </header>

      <div className="queue-bulk-panel">
        <p className="muted" id="queue-shuffle-liked-description">{t("queue.shuffleLikedDescription")}</p>
        <button
          className="button button-primary"
          type="button"
          aria-describedby="queue-shuffle-liked-description"
          disabled={!queue || Boolean(busyEntry)}
          onClick={() => void mutate("", "append_liked_shuffled")}
        >
          {t("queue.shuffleLiked")}
        </button>
      </div>

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
                  {entry.status === "error" && <em>{t("queue.playbackFailed")}</em>}
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
                    className="icon-button library-like-button"
                    type="button"
                    aria-label={t(entry.track.liked ? "library.unlikeTrack" : "library.likeTrack", { title: trackTitle })}
                    title={t(entry.track.liked ? "library.unlikeTitle" : "library.likeTitle")}
                    aria-pressed={entry.track.liked}
                    disabled={likeBusy.has(entry.track_id)}
                    onClick={() => void toggleLiked(entry.track)}
                  >
                    <QueueIcon name="heart" />
                  </button>
                  <button
                    className="icon-button"
                    type="button"
                    aria-label={t("queue.moveUp")}
                    title={t("queue.moveUp")}
                    disabled={index === 0 || locked || busy || Boolean(busyEntry)}
                    onClick={() => void mutate(entry.id, "move", index - 1)}
                  >
                    <QueueIcon name="up" />
                  </button>
                  <button
                    className="icon-button"
                    type="button"
                    aria-label={t("queue.moveDown")}
                    title={t("queue.moveDown")}
                    disabled={index === queue.entries.length - 1 || locked || busy || Boolean(busyEntry)}
                    onClick={() => void mutate(entry.id, "move", index + 1)}
                  >
                    <QueueIcon name="down" />
                  </button>
                  <button
                    className="icon-button danger-button"
                    type="button"
                    aria-label={t("queue.remove")}
                    title={t("queue.remove")}
                    disabled={busy || Boolean(busyEntry)}
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
      <TrackInfoDialog trackId={infoTrackID} revision={revision} onNotice={onNotice} onClose={() => setInfoTrackID(null)} downloads={downloads} />
    </section>
  );
}
