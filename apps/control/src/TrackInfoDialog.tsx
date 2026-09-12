import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api } from "./api";
import { useI18n } from "./i18n";
import type { Track, TrackInfo } from "./types";
import "./TrackInfoDialog.css";

type Props = {
  trackId: string | null;
  onClose: () => void;
};

type LoadedInfo = {
  trackId: string;
  value: TrackInfo;
};

type FailedInfo = {
  trackId: string;
  message: string | null;
};

function present(value: string, unavailable: string): string {
  return value.trim() ? value : unavailable;
}

function formatDuration(milliseconds: number, unavailable: string, locale: string): string {
  if (!Number.isFinite(milliseconds) || milliseconds <= 0) return unavailable;
  const totalSeconds = Math.floor(milliseconds / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const twoDigit = { minimumIntegerDigits: 2, useGrouping: false } as const;
  return hours > 0
    ? `${hours.toLocaleString(locale, { useGrouping: false })}:${minutes.toLocaleString(locale, twoDigit)}:${seconds.toLocaleString(locale, twoDigit)}`
    : `${minutes.toLocaleString(locale, { useGrouping: false })}:${seconds.toLocaleString(locale, twoDigit)}`;
}

function formatSampleRate(value: number | null, unavailable: string, locale: string): string {
  if (value === null || !Number.isFinite(value) || value <= 0) return unavailable;
  const hz = value.toLocaleString(locale);
  if (value < 1_000) return `${hz} Hz`;
  const khz = (value / 1_000).toLocaleString(locale, { maximumFractionDigits: 3 });
  return `${khz} kHz (${hz} Hz)`;
}

function formatBitRate(value: number | null, unavailable: string, locale: string): string {
  if (value === null || !Number.isFinite(value) || value <= 0) return unavailable;
  const kbps = (value / 1_000).toLocaleString(locale, { maximumFractionDigits: 1 });
  return `${kbps} kbps`;
}

function formatFileSize(bytes: number, unavailable: string, locale: string): string {
  if (!Number.isFinite(bytes) || bytes < 0) return unavailable;
  if (bytes === 0) return `${bytes.toLocaleString(locale)} B`;
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const unitIndex = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / (1024 ** unitIndex);
  return `${value.toLocaleString(locale, { maximumFractionDigits: unitIndex === 0 ? 0 : 2 })} ${units[unitIndex]}`;
}

function formatModifiedAt(value: string, unavailable: string, locale: string): string {
  if (!value.trim()) return unavailable;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(locale);
}

function Artwork({ track }: { track: Track }) {
  const { t } = useI18n();
  const [failedArtworkID, setFailedArtworkID] = useState("");
  const artworkID = track.artwork_id;
  const title = track.title.trim() ? track.title : t("info.unavailable");
  if (!artworkID || failedArtworkID === artworkID) {
    return (
      <span className="track-info-artwork track-info-artwork-empty" role="img" aria-label={t("info.artworkMissing", { title })}>
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M9 18V5l11-2v13" />
          <circle cx="6" cy="18" r="3" />
          <circle cx="17" cy="16" r="3" />
        </svg>
      </span>
    );
  }
  return (
    <img
      className="track-info-artwork"
      src={`/api/v1/artwork/${encodeURIComponent(artworkID)}`}
      alt={t("info.artwork", { title })}
      onError={() => setFailedArtworkID(artworkID)}
    />
  );
}

function Detail({ label, value, wide = false, code = false }: { label: string; value: string; wide?: boolean; code?: boolean }) {
  return (
    <div className={`track-info-detail${wide ? " track-info-detail-wide" : ""}`}>
      <dt>{label}</dt>
      <dd className={code ? "track-info-code" : undefined}>{value}</dd>
    </div>
  );
}

export default function TrackInfoDialog({ trackId, onClose }: Props) {
  const { locale, t } = useI18n();
  const [loaded, setLoaded] = useState<LoadedInfo | null>(null);
  const [failure, setFailure] = useState<FailedInfo | null>(null);
  const [requestVersion, setRequestVersion] = useState(0);
  const dialogRef = useRef<HTMLElement | null>(null);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const currentTrackIDRef = useRef(trackId);
  currentTrackIDRef.current = trackId;
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  const open = trackId !== null;

  useEffect(() => {
    if (!open) return;
    previousFocusRef.current = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null;
    if (!document.querySelector('[role="alertdialog"][aria-modal="true"]')) {
      closeRef.current?.focus();
    }

    function handleKeyDown(event: KeyboardEvent) {
      if (document.querySelector('[role="alertdialog"][aria-modal="true"]')) return;
      if (event.key === "Escape") {
        event.preventDefault();
        onCloseRef.current();
        return;
      }
      if (event.key !== "Tab") return;

      const focusable = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>(
        'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])',
      ) ?? []).filter((element) => !element.hasAttribute("hidden"));
      if (focusable.length === 0) {
        event.preventDefault();
        dialogRef.current?.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && (document.activeElement === first || !dialogRef.current?.contains(document.activeElement))) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && (document.activeElement === last || !dialogRef.current?.contains(document.activeElement))) {
        event.preventDefault();
        first.focus();
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      const previousFocus = previousFocusRef.current;
      previousFocusRef.current = null;
      if (document.querySelector('[role="alertdialog"][aria-modal="true"]')) return;
      if (previousFocus?.isConnected) previousFocus.focus();
    };
  }, [open]);

  useEffect(() => {
    if (trackId === null) return;
    const requestedTrackID = trackId;
    const controller = new AbortController();
    let active = true;
    setFailure(null);

    api<TrackInfo>(`/library/tracks/${encodeURIComponent(requestedTrackID)}/info`, { signal: controller.signal })
      .then((value) => {
        if (active && currentTrackIDRef.current === requestedTrackID) setLoaded({ trackId: requestedTrackID, value });
      })
      .catch((caught: unknown) => {
        if (!active || currentTrackIDRef.current !== requestedTrackID || (caught instanceof DOMException && caught.name === "AbortError")) return;
        const message = caught && typeof caught === "object" && "message" in caught
          ? String(caught.message)
          : null;
        setFailure({ trackId: requestedTrackID, message });
      });

    return () => {
      active = false;
      controller.abort();
    };
  }, [requestVersion, trackId]);

  if (trackId === null) return null;

  const info = loaded?.trackId === trackId ? loaded.value : null;
  const error = failure?.trackId === trackId ? failure.message ?? t("info.loadFailed") : "";
  const track = info?.track;
  const unavailable = t("info.unavailable");
  const tags = info
    ? Object.entries(info.tags).sort(([left], [right]) => left.localeCompare(right, locale))
    : [];

  return createPortal(
    <div
      className="track-info-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.currentTarget === event.target) onClose();
      }}
    >
      <section
        ref={dialogRef}
        className="track-info-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="track-info-title"
        aria-describedby={error ? "track-info-error" : undefined}
        tabIndex={-1}
      >
        <header className="track-info-heading">
          <div>
            <p className="track-info-eyebrow">{t("info.title")}</p>
            <h2 id="track-info-title">{track?.title || t("info.title")}</h2>
          </div>
          <button ref={closeRef} className="track-info-close" type="button" aria-label={t("info.close")} title={t("common.close")} onClick={onClose}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
          </button>
        </header>

        {!info && !error && <div className="track-info-status" role="status">{t("info.loading")}</div>}

        {error && (
          <div className="track-info-error" id="track-info-error" role="alert">
            <p>{error}</p>
            <button className="button button-ghost" type="button" onClick={() => setRequestVersion((value) => value + 1)}>{t("common.retry")}</button>
          </div>
        )}

        {track && info && (
          <div className="track-info-content">
            <div className="track-info-summary">
              <Artwork key={track.artwork_id} track={track} />
              <div>
                <strong>{present(track.title, unavailable)}</strong>
                <span>{present(track.artist, unavailable)}</span>
                <span>{present(track.album, unavailable)}</span>
              </div>
            </div>

            <section className="track-info-section" aria-labelledby="track-info-music-heading">
              <h3 id="track-info-music-heading">{t("info.music")}</h3>
              <dl className="track-info-grid">
                <Detail label={t("info.field.title")} value={present(track.title, unavailable)} />
                <Detail label={t("info.field.artist")} value={present(track.artist, unavailable)} />
                <Detail label={t("info.field.album")} value={present(track.album, unavailable)} />
                <Detail label={t("info.field.albumArtist")} value={present(track.album_artist, unavailable)} />
                <Detail label={t("info.field.disc")} value={track.disc > 0 ? track.disc.toLocaleString(locale) : unavailable} />
                <Detail label={t("info.field.track")} value={track.track > 0 ? track.track.toLocaleString(locale) : unavailable} />
                <Detail label={t("info.field.genre")} value={track.genres.length ? track.genres.join(", ") : unavailable} wide />
              </dl>
            </section>

            <section className="track-info-section" aria-labelledby="track-info-audio-heading">
              <h3 id="track-info-audio-heading">{t("info.audio")}</h3>
              <dl className="track-info-grid">
                <Detail label={t("info.field.codec")} value={present(info.audio.codec, unavailable)} />
                <Detail label={t("info.field.sampleRate")} value={formatSampleRate(info.audio.sample_rate, unavailable, locale)} />
                <Detail label={t("info.field.channels")} value={info.audio.channels !== null && info.audio.channels > 0 ? t(info.audio.channels === 1 ? "info.channelCountOne" : "info.channelCount", { count: info.audio.channels.toLocaleString(locale) }) : unavailable} />
                <Detail label={t("info.field.bitDepth")} value={info.audio.bits_per_sample !== null && info.audio.bits_per_sample > 0 ? t("info.bitDepth", { count: info.audio.bits_per_sample.toLocaleString(locale) }) : unavailable} />
                <Detail label={t("info.field.bitRate")} value={formatBitRate(info.audio.bit_rate, unavailable, locale)} />
                <Detail label={t("info.field.duration")} value={formatDuration(track.duration_ms, unavailable, locale)} />
              </dl>
            </section>

            <section className="track-info-section" aria-labelledby="track-info-file-heading">
              <h3 id="track-info-file-heading">{t("info.file")}</h3>
              <dl className="track-info-grid">
                <Detail label={t("info.field.format")} value={present(track.format, unavailable)} />
                <Detail label={t("info.field.mime")} value={present(track.mime, unavailable)} />
                <Detail label={t("info.field.size")} value={formatFileSize(track.size, unavailable, locale)} />
                <Detail label={t("info.field.status")} value={track.available ? t("info.available") : t("info.fileUnavailable")} />
                <Detail label={t("info.field.modifiedAt")} value={formatModifiedAt(track.modified_at, unavailable, locale)} wide />
                <Detail label={t("info.field.path")} value={present(track.path, unavailable)} wide code />
              </dl>
            </section>

            <section className="track-info-section" aria-labelledby="track-info-tags-heading">
              <h3 id="track-info-tags-heading">{t("info.embeddedMetadata")}</h3>
              {tags.length ? (
                <dl className="track-info-tags">
                  {tags.map(([name, values]) => (
                    <div key={name}>
                      <dt>{name}</dt>
                      <dd>{values.length ? values.map((value, index) => <span key={`${index}:${value}`}>{value || unavailable}</span>) : unavailable}</dd>
                    </div>
                  ))}
                </dl>
              ) : <p className="track-info-empty">{t("info.noEmbeddedMetadata")}</p>}
            </section>
          </div>
        )}
      </section>
    </div>,
    document.body,
  );
}
