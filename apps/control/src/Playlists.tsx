import { useEffect, useMemo, useRef, useState } from "react";
import { addTracks, api, playTracks } from "./api";
import type { Playlist, Track } from "./types";
import { useI18n } from "./i18n";
import "./library.css";

type Props = {
  revision: number;
  onNotice: (message: string, error?: boolean) => void;
  onQueueChange: () => void;
};

type DraftEntry = {
  key: string;
  trackID: string;
  track: Track | undefined;
};

function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function isConflict(error: unknown): boolean {
  return Boolean(error && typeof error === "object" && "status" in error && error.status === 409);
}

function errorMessage(error: unknown, fallback: string): string {
  if (error && typeof error === "object" && "message" in error) return String(error.message);
  return fallback;
}

function formatDuration(milliseconds: number): string {
  if (!milliseconds) return "—";
  const totalSeconds = Math.floor(milliseconds / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`
    : `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function Icon({ name }: { name: "playlist" | "play" | "append" | "up" | "down" | "remove" | "save" | "trash" | "plus" }) {
  const paths = {
    playlist: <><path d="M4 6h12M4 11h12M4 16h7" /><path d="M18 14v7m-3.5-3.5h7" /></>,
    play: <path d="m8 5 11 7-11 7z" />,
    append: <><path d="M4 6h10M4 12h10M4 18h7" /><path d="M18 14v7m-3.5-3.5h7" /></>,
    up: <path d="m6 15 6-6 6 6" />,
    down: <path d="m6 9 6 6 6-6" />,
    remove: <><path d="M5 12h14" /><circle cx="12" cy="12" r="9" /></>,
    save: <><path d="M5 4h12l2 2v14H5z" /><path d="M8 4v6h8V4M8 20v-6h8v6" /></>,
    trash: <><path d="M4 7h16M9 7V4h6v3M7 7l1 13h8l1-13" /></>,
    plus: <path d="M12 5v14M5 12h14" />,
  };
  return <svg className="playlist-icon" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{paths[name]}</svg>;
}

function makeEntries(playlist: Playlist): DraftEntry[] {
  return playlist.track_ids.map((trackID, index) => ({
    key: `${playlist.revision}:${index}:${trackID}`,
    trackID,
    track: (playlist.tracks ?? [])[index],
  }));
}

function sameTrackOrder(entries: DraftEntry[], playlist: Playlist): boolean {
  return entries.length === playlist.track_ids.length && entries.every((entry, index) => entry.trackID === playlist.track_ids[index]);
}

export default function Playlists({ revision, onNotice, onQueueChange }: Props) {
  const { locale, t } = useI18n();
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale]);
  const [playlists, setPlaylists] = useState<Playlist[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [baseline, setBaseline] = useState<Playlist | null>(null);
  const [draftName, setDraftName] = useState("");
  const [entries, setEntries] = useState<DraftEntry[]>([]);
  const [pendingRemote, setPendingRemote] = useState<Playlist | null>(null);
  const [newName, setNewName] = useState("");
  const [loadingList, setLoadingList] = useState(true);
  const [loadingDetail, setLoadingDetail] = useState(false);
  const [busy, setBusy] = useState(false);
  const [listError, setListError] = useState("");
  const [detailError, setDetailError] = useState("");
  const [createError, setCreateError] = useState("");
  const [deleteArmed, setDeleteArmed] = useState(false);
  const [reload, setReload] = useState(0);
  const baselineRef = useRef<Playlist | null>(null);
  const dirtyRef = useRef(false);

  const dirty = Boolean(baseline && (draftName.trim() !== baseline.name || !sameTrackOrder(entries, baseline)));
  dirtyRef.current = dirty;
  baselineRef.current = baseline;

  function applyAuthoritative(playlist: Playlist) {
    setBaseline(playlist);
    setDraftName(playlist.name);
    setEntries(makeEntries(playlist));
    setPendingRemote(null);
    setDetailError("");
    setDeleteArmed(false);
  }

  useEffect(() => {
    const controller = new AbortController();
    setLoadingList(true);
    setListError("");
    api<{ items: Playlist[] }>("/playlists", { signal: controller.signal })
      .then((result) => {
        if (controller.signal.aborted) return;
        setPlaylists(result.items);
        setSelectedID((current) => result.items.some((playlist) => playlist.id === current) ? current : result.items[0]?.id ?? "");
      })
      .catch((caught: unknown) => {
        if (!controller.signal.aborted && !isAbort(caught)) setListError(errorMessage(caught, t("common.requestFailed")));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoadingList(false);
      });
    return () => controller.abort();
  }, [reload, revision, t]);

  useEffect(() => {
    if (!selectedID) {
      setBaseline(null);
      setEntries([]);
      setDraftName("");
      return;
    }
    const controller = new AbortController();
    setLoadingDetail(true);
    setDetailError("");
    api<Playlist>(`/playlists/${encodeURIComponent(selectedID)}`, { signal: controller.signal })
      .then((playlist) => {
        if (controller.signal.aborted) return;
        const current = baselineRef.current;
        if (dirtyRef.current && current?.id === playlist.id) {
          if (current.revision !== playlist.revision) {
            setPendingRemote(playlist);
          } else {
            const tracksByID = new Map((playlist.tracks ?? []).map((track) => [track.id, track]));
            setBaseline(playlist);
            setEntries((draft) => draft.map((entry) => ({ ...entry, track: tracksByID.get(entry.trackID) ?? entry.track })));
          }
          return;
        }
        applyAuthoritative(playlist);
      })
      .catch((caught: unknown) => {
        if (!controller.signal.aborted && !isAbort(caught)) setDetailError(errorMessage(caught, t("common.requestFailed")));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoadingDetail(false);
      });
    return () => controller.abort();
  }, [reload, selectedID, revision, t]);

  async function refreshConflict() {
    if (!selectedID) return;
    try {
      const latest = await api<Playlist>(`/playlists/${encodeURIComponent(selectedID)}`);
      setPendingRemote(latest);
      setDetailError(t("playlists.conflictChoice"));
    } catch (caught) {
      setDetailError(errorMessage(caught, t("common.requestFailed")));
    }
  }

  function selectPlaylist(id: string) {
    if (id === selectedID) return;
    if (dirty || pendingRemote) {
      setDetailError(t("playlists.finishEditing"));
      return;
    }
    setSelectedID(id);
    setBaseline(null);
    setEntries([]);
    setDraftName("");
    setPendingRemote(null);
    setDeleteArmed(false);
  }

  async function createPlaylist() {
    const name = newName.trim();
    if (!name) return;
    if (dirty || pendingRemote) {
      setCreateError(t("playlists.finishBeforeCreate"));
      return;
    }
    setBusy(true);
    setCreateError("");
    try {
      const created = await api<Playlist>("/playlists", {
        method: "POST",
        body: JSON.stringify({ name, track_ids: [] }),
      });
      setPlaylists((current) => [...current, created]);
      setNewName("");
      setSelectedID(created.id);
      applyAuthoritative(created);
      onNotice(t("playlists.created", { name: created.name }));
    } catch (caught) {
      setCreateError(errorMessage(caught, t("common.requestFailed")));
    } finally {
      setBusy(false);
    }
  }

  async function savePlaylist() {
    if (!baseline || pendingRemote) return;
    const name = draftName.trim();
    if (!name) {
      setDetailError(t("playlists.nameRequired"));
      return;
    }
    setBusy(true);
    setDetailError("");
    try {
      const saved = await api<Playlist>(`/playlists/${encodeURIComponent(baseline.id)}`, {
        method: "PUT",
        body: JSON.stringify({ name, track_ids: entries.map((entry) => entry.trackID), revision: baseline.revision }),
      });
      applyAuthoritative(saved);
      setPlaylists((current) => current.map((playlist) => playlist.id === saved.id ? saved : playlist));
      onNotice(t("playlists.saved"));
    } catch (caught) {
      if (isConflict(caught)) await refreshConflict();
      else setDetailError(errorMessage(caught, t("common.requestFailed")));
    } finally {
      setBusy(false);
    }
  }

  async function deletePlaylist() {
    if (!baseline) return;
    setBusy(true);
    setDetailError("");
    try {
      await api<void>(`/playlists/${encodeURIComponent(baseline.id)}?revision=${encodeURIComponent(String(baseline.revision))}`, { method: "DELETE" });
      const remaining = playlists.filter((playlist) => playlist.id !== baseline.id);
      setPlaylists(remaining);
      setSelectedID(remaining[0]?.id ?? "");
      setBaseline(null);
      setEntries([]);
      setDeleteArmed(false);
      onNotice(t("playlists.deleted", { name: baseline.name }));
    } catch (caught) {
      if (isConflict(caught)) await refreshConflict();
      else setDetailError(errorMessage(caught, t("common.requestFailed")));
    } finally {
      setBusy(false);
    }
  }

  function moveEntry(index: number, direction: -1 | 1) {
    const target = index + direction;
    if (target < 0 || target >= entries.length) return;
    setEntries((current) => {
      const reordered = [...current];
      [reordered[index], reordered[target]] = [reordered[target], reordered[index]];
      return reordered;
    });
  }

  async function queuePlaylist(action: "play" | "append") {
    const availableIDs = entries.filter((entry) => entry.track?.available).map((entry) => entry.trackID);
    if (!availableIDs.length) {
      onNotice(t("playlists.noPlayable"), true);
      return;
    }
    setBusy(true);
    try {
      if (action === "play") await playTracks(availableIDs);
      else await addTracks(availableIDs, "append");
      onQueueChange();
      onNotice(t(action === "play" ? "playlists.playing" : "playlists.appended"));
    } catch (caught) {
      onNotice(errorMessage(caught, t("common.requestFailed")), true);
    } finally {
      setBusy(false);
    }
  }

  function loadRemoteVersion() {
    if (pendingRemote) applyAuthoritative(pendingRemote);
  }

  function keepDraftOnLatestRevision() {
    if (!pendingRemote) return;
    setBaseline(pendingRemote);
    setPendingRemote(null);
    setDetailError("");
  }
  function discardDraft() {
    if (pendingRemote) {
      applyAuthoritative(pendingRemote);
    } else if (baseline) {
      applyAuthoritative(baseline);
    }
    setCreateError("");
  }


  const availableCount = entries.filter((entry) => entry.track?.available).length;
  const totalDuration = useMemo(() => entries.reduce((sum, entry) => sum + (entry.track?.duration_ms ?? 0), 0), [entries]);

  return (
    <section className="playlist-shell" aria-labelledby="playlists-heading">
      <header className="playlist-page-heading">
        <div><p className="playlist-eyebrow">{t("playlists.eyebrow")}</p><h1 id="playlists-heading" className="page-heading">{t("playlists.heading")}</h1><p className="muted">{t("playlists.description")}</p></div>
      </header>

      <div className="playlist-layout">
        <aside className="playlist-sidebar" aria-label={t("playlists.savedLabel")}>
          <form className="playlist-create" onSubmit={(event) => { event.preventDefault(); void createPlaylist(); }}>
            <label htmlFor="playlist-new-name">{t("playlists.new")}</label>
            <div><input id="playlist-new-name" className="input" value={newName} onChange={(event) => setNewName(event.target.value)} maxLength={120} placeholder={t("playlists.namePlaceholder")} /><button className="button button-primary" type="submit" disabled={busy || !newName.trim()} aria-label={t("playlists.createLabel")}><Icon name="plus" /> {t("playlists.create")}</button></div>
            {createError && <p className="error-text" role="alert">{createError}</p>}
          </form>

          {loadingList && <p className="playlist-loading" role="status">{t("playlists.loadingList")}</p>}
          {listError && <div className="playlist-inline-error" role="alert"><p className="error-text">{listError}</p><button className="button button-ghost" type="button" onClick={() => setReload((current) => current + 1)}>{t("common.retry")}</button></div>}
          {!loadingList && !listError && playlists.length === 0 && <div className="empty-state">{t("playlists.empty")}</div>}
          <div className="playlist-list">
            {playlists.map((playlist) => {
              const trackCount = playlist.track_ids?.length ?? playlist.tracks?.length ?? 0;
              return (
                <button className={`playlist-list-item${selectedID === playlist.id ? " playlist-list-item-active" : ""}`} type="button" key={playlist.id} onClick={() => selectPlaylist(playlist.id)} aria-current={selectedID === playlist.id ? "page" : undefined}>
                  <span className="playlist-list-icon"><Icon name="playlist" /></span><span><strong>{playlist.name}</strong><small>{t(trackCount === 1 ? "library.oneTrack" : "library.manyTracks", { count: numberFormatter.format(trackCount) })}</small></span>
                </button>
              );
            })}
          </div>
        </aside>

        <main className="playlist-editor">
          {!selectedID && !loadingList && <div className="empty-state">{t("playlists.selectPrompt")}</div>}
          {selectedID && !loadingDetail && !baseline && detailError && <div className="playlist-inline-error" role="alert"><p className="error-text">{detailError}</p><button className="button button-ghost" type="button" onClick={() => setReload((current) => current + 1)}>{t("common.retry")}</button></div>}
          {selectedID && loadingDetail && !baseline && <p className="playlist-loading" role="status">{t("playlists.loadingDetail")}</p>}
          {baseline && (
            <>
              <header className="playlist-editor-heading">
                <div className="playlist-title-field"><label htmlFor="playlist-title">{t("playlists.name")}</label><input id="playlist-title" className="input" value={draftName} onChange={(event) => setDraftName(event.target.value)} maxLength={120} /></div>
                <p className="playlist-summary">{t(entries.length === 1 ? "playlists.summaryOne" : "playlists.summaryMany", { count: numberFormatter.format(entries.length), available: numberFormatter.format(availableCount), duration: formatDuration(totalDuration) })}</p>
                <div className="playlist-primary-actions">
                  <button className="button button-primary" type="button" disabled={busy || !availableCount} onClick={() => queuePlaylist("play")}><Icon name="play" /> {t("playlists.play")}</button>
                  <button className="button button-ghost" type="button" disabled={busy || !availableCount} onClick={() => queuePlaylist("append")}><Icon name="append" /> {t("playlists.append")}</button>
                  <button className="button button-ghost" type="button" disabled={busy || !dirty || Boolean(pendingRemote) || !draftName.trim()} onClick={savePlaylist}><Icon name="save" /> {t("playlists.saveChanges")}</button>
                  <button className="button button-ghost" type="button" disabled={busy || (!dirty && !pendingRemote)} onClick={discardDraft}>{t("playlists.cancelChanges")}</button>
                </div>
              </header>

              {pendingRemote && (
                <div className="playlist-conflict" role="alert">
                  <div><strong>{t("playlists.changedElsewhere")}</strong><p>{t("playlists.conflictDescription")}</p></div>
                  <div><button className="button button-ghost" type="button" onClick={loadRemoteVersion}>{t("playlists.loadServerChanges")}</button><button className="button button-primary" type="button" onClick={keepDraftOnLatestRevision}>{t("playlists.reapplyEdits")}</button></div>
                </div>
              )}
              {detailError && !pendingRemote && <p className="error-text playlist-detail-error" role="alert">{detailError}</p>}

              {entries.length === 0 ? <div className="empty-state">{t("playlists.noTracks")}</div> : (
                <div className="playlist-entry-list" role="list" aria-label={t("playlists.entriesLabel", { name: draftName || baseline.name })}>
                  {entries.map((entry, index) => {
                    const track = entry.track;
                    const trackTitle = track?.title ?? t("playlists.unknownTrack");
                    return (
                      <article className={`playlist-entry${track?.available === false ? " playlist-entry-unavailable" : ""}`} role="listitem" key={entry.key}>
                        <span className="playlist-entry-number">{numberFormatter.format(index + 1)}</span>
                        <div className="playlist-entry-copy">
                          <strong>{track?.title ?? t("playlists.trackUnavailable")}</strong>
                          <span>{track ? <>{track.artist || t("library.unknownArtist")}{track.album && <> · {track.album}</>}</> : entry.trackID}</span>
                          {track?.available === false && <small>{t("playlists.sourceUnavailable")}</small>}
                        </div>
                        <time className="playlist-entry-duration">{formatDuration(track?.duration_ms ?? 0)}</time>
                        <div className="playlist-entry-actions" aria-label={t("playlists.entryActions", { title: trackTitle })}>
                          <button type="button" disabled={busy || index === 0} onClick={() => moveEntry(index, -1)} aria-label={t("playlists.moveUp", { title: trackTitle })} title={t("playlists.moveUpTitle")}><Icon name="up" /></button>
                          <button type="button" disabled={busy || index === entries.length - 1} onClick={() => moveEntry(index, 1)} aria-label={t("playlists.moveDown", { title: trackTitle })} title={t("playlists.moveDownTitle")}><Icon name="down" /></button>
                          <button className="playlist-remove-button" type="button" disabled={busy} onClick={() => setEntries((current) => current.filter((_, currentIndex) => currentIndex !== index))} aria-label={t("playlists.removeTrack", { title: trackTitle })} title={t("playlists.removeTrackTitle")}><Icon name="remove" /></button>
                        </div>
                      </article>
                    );
                  })}
                </div>
              )}

              <footer className="playlist-danger-zone">
                <div><strong>{t("playlists.deleteHeading")}</strong><p>{t("playlists.deleteDescription")}</p></div>
                {!deleteArmed ? <button className="button button-ghost playlist-danger-button" type="button" disabled={busy} onClick={() => setDeleteArmed(true)}><Icon name="trash" /> {t("common.delete")}</button> : <div className="playlist-delete-confirm"><span>{t("playlists.deleteConfirm")}</span><button className="button button-ghost" type="button" onClick={() => setDeleteArmed(false)}>{t("common.cancel")}</button><button className="button playlist-danger-button" type="button" disabled={busy} onClick={deletePlaylist}>{t("playlists.deleteList")}</button></div>}
              </footer>
            </>
          )}
        </main>
      </div>
    </section>
  );
}
