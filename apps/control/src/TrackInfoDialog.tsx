import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api } from "./api";
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
  message: string;
};

const unavailable = "정보 없음";
function present(value: string): string {
  return value.trim() || unavailable;
}

function formatDuration(milliseconds: number): string {
  if (!Number.isFinite(milliseconds) || milliseconds <= 0) return unavailable;
  const totalSeconds = Math.floor(milliseconds / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`
    : `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function formatSampleRate(value: number | null): string {
  if (value === null || !Number.isFinite(value) || value <= 0) return unavailable;
  const hz = value.toLocaleString("ko-KR");
  if (value < 1_000) return `${hz} Hz`;
  const khz = (value / 1_000).toLocaleString("ko-KR", { maximumFractionDigits: 3 });
  return `${khz} kHz (${hz} Hz)`;
}

function formatBitRate(value: number | null): string {
  if (value === null || !Number.isFinite(value) || value <= 0) return unavailable;
  const kbps = (value / 1_000).toLocaleString("ko-KR", { maximumFractionDigits: 1 });
  return `${kbps} kbps`;
}

function formatFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return unavailable;
  if (bytes === 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  const unitIndex = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / (1024 ** unitIndex);
  return `${value.toLocaleString("ko-KR", { maximumFractionDigits: unitIndex === 0 ? 0 : 2 })} ${units[unitIndex]}`;
}

function formatModifiedAt(value: string): string {
  if (!value.trim()) return unavailable;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("ko-KR");
}

function Artwork({ track }: { track: Track }) {
  const [failedArtworkID, setFailedArtworkID] = useState("");
  const artworkID = track.artwork_id;
  if (!artworkID || failedArtworkID === artworkID) {
    return (
      <span className="track-info-artwork track-info-artwork-empty" role="img" aria-label={`${track.title} 아트워크 없음`}>
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
      alt={`${track.title} 아트워크`}
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
          : "곡 정보를 불러오지 못했습니다.";
        setFailure({ trackId: requestedTrackID, message });
      });

    return () => {
      active = false;
      controller.abort();
    };
  }, [requestVersion, trackId]);

  if (trackId === null) return null;

  const info = loaded?.trackId === trackId ? loaded.value : null;
  const error = failure?.trackId === trackId ? failure.message : "";
  const track = info?.track;
  const tags = info
    ? Object.entries(info.tags).sort(([left], [right]) => left.localeCompare(right, "ko"))
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
            <p className="track-info-eyebrow">곡 정보</p>
            <h2 id="track-info-title">{track ? track.title : "곡 정보"}</h2>
          </div>
          <button ref={closeRef} className="track-info-close" type="button" aria-label="곡 정보 닫기" title="닫기" onClick={onClose}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18" /></svg>
          </button>
        </header>

        {!info && !error && <div className="track-info-status" role="status">곡 정보를 불러오는 중…</div>}

        {error && (
          <div className="track-info-error" id="track-info-error" role="alert">
            <p>{error}</p>
            <button className="button button-ghost" type="button" onClick={() => setRequestVersion((value) => value + 1)}>다시 시도</button>
          </div>
        )}

        {track && info && (
          <div className="track-info-content">
            <div className="track-info-summary">
              <Artwork key={track.artwork_id} track={track} />
              <div>
                <strong>{present(track.title)}</strong>
                <span>{present(track.artist)}</span>
                <span>{present(track.album)}</span>
              </div>
            </div>

            <section className="track-info-section" aria-labelledby="track-info-music-heading">
              <h3 id="track-info-music-heading">음악</h3>
              <dl className="track-info-grid">
                <Detail label="제목" value={present(track.title)} />
                <Detail label="아티스트" value={present(track.artist)} />
                <Detail label="앨범" value={present(track.album)} />
                <Detail label="앨범 아티스트" value={present(track.album_artist)} />
                <Detail label="디스크" value={track.disc > 0 ? track.disc.toLocaleString("ko-KR") : unavailable} />
                <Detail label="트랙" value={track.track > 0 ? track.track.toLocaleString("ko-KR") : unavailable} />
                <Detail label="장르" value={track.genres.length ? track.genres.join(", ") : unavailable} wide />
              </dl>
            </section>

            <section className="track-info-section" aria-labelledby="track-info-audio-heading">
              <h3 id="track-info-audio-heading">오디오</h3>
              <dl className="track-info-grid">
                <Detail label="코덱" value={present(info.audio.codec)} />
                <Detail label="샘플 레이트" value={formatSampleRate(info.audio.sample_rate)} />
                <Detail label="채널" value={info.audio.channels !== null && info.audio.channels > 0 ? `${info.audio.channels.toLocaleString("ko-KR")}채널` : unavailable} />
                <Detail label="비트 깊이" value={info.audio.bits_per_sample !== null && info.audio.bits_per_sample > 0 ? `${info.audio.bits_per_sample.toLocaleString("ko-KR")}비트` : unavailable} />
                <Detail label="비트레이트" value={formatBitRate(info.audio.bit_rate)} />
                <Detail label="재생 시간" value={formatDuration(track.duration_ms)} />
              </dl>
            </section>

            <section className="track-info-section" aria-labelledby="track-info-file-heading">
              <h3 id="track-info-file-heading">파일</h3>
              <dl className="track-info-grid">
                <Detail label="형식" value={present(track.format)} />
                <Detail label="MIME 형식" value={present(track.mime)} />
                <Detail label="크기" value={formatFileSize(track.size)} />
                <Detail label="상태" value={track.available ? "사용 가능" : "파일을 사용할 수 없음"} />
                <Detail label="수정 시각" value={formatModifiedAt(track.modified_at)} wide />
                <Detail label="경로" value={present(track.path)} wide code />
              </dl>
            </section>

            <section className="track-info-section" aria-labelledby="track-info-tags-heading">
              <h3 id="track-info-tags-heading">임베디드 메타데이터</h3>
              {tags.length ? (
                <dl className="track-info-tags">
                  {tags.map(([name, values]) => (
                    <div key={name}>
                      <dt>{name}</dt>
                      <dd>{values.length ? values.map((value, index) => <span key={`${index}:${value}`}>{value || unavailable}</span>) : unavailable}</dd>
                    </div>
                  ))}
                </dl>
              ) : <p className="track-info-empty">표시할 임베디드 메타데이터가 없습니다.</p>}
            </section>
          </div>
        )}
      </section>
    </div>,
    document.body,
  );
}
