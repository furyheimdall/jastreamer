import { useEffect, useMemo, useRef, useState } from "react";
import { addTracks, api, playTracks } from "./api";
import type { Playlist, Track } from "./types";
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

function errorMessage(error: unknown): string {
  if (error && typeof error === "object" && "message" in error) return String(error.message);
  return "요청을 완료하지 못했습니다.";
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
        if (!controller.signal.aborted && !isAbort(caught)) setListError(errorMessage(caught));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoadingList(false);
      });
    return () => controller.abort();
  }, [reload, revision]);

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
        if (!controller.signal.aborted && !isAbort(caught)) setDetailError(errorMessage(caught));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoadingDetail(false);
      });
    return () => controller.abort();
  }, [reload, selectedID, revision]);

  async function refreshConflict() {
    if (!selectedID) return;
    try {
      const latest = await api<Playlist>(`/playlists/${encodeURIComponent(selectedID)}`);
      setPendingRemote(latest);
      setDetailError("다른 곳에서 이 플레이리스트를 변경했습니다. 어느 버전을 유지할지 선택해 주세요.");
    } catch (caught) {
      setDetailError(errorMessage(caught));
    }
  }

  function selectPlaylist(id: string) {
    if (id === selectedID) return;
    if (dirty || pendingRemote) {
      setDetailError("현재 편집을 저장하거나 취소한 뒤 다른 플레이리스트를 선택해 주세요.");
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
      setCreateError("현재 플레이리스트 편집을 먼저 저장하거나 취소해 주세요.");
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
      onNotice(`‘${created.name}’ 플레이리스트를 만들었습니다.`);
    } catch (caught) {
      setCreateError(errorMessage(caught));
    } finally {
      setBusy(false);
    }
  }

  async function savePlaylist() {
    if (!baseline || pendingRemote) return;
    const name = draftName.trim();
    if (!name) {
      setDetailError("플레이리스트 이름을 입력해 주세요.");
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
      onNotice("플레이리스트를 저장했습니다.");
    } catch (caught) {
      if (isConflict(caught)) await refreshConflict();
      else setDetailError(errorMessage(caught));
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
      onNotice(`‘${baseline.name}’ 플레이리스트를 삭제했습니다. 원본 음악 파일은 그대로 유지됩니다.`);
    } catch (caught) {
      if (isConflict(caught)) await refreshConflict();
      else setDetailError(errorMessage(caught));
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
      onNotice("재생 가능한 곡이 없습니다.", true);
      return;
    }
    setBusy(true);
    try {
      if (action === "play") await playTracks(availableIDs);
      else await addTracks(availableIDs, "append");
      onQueueChange();
      onNotice(action === "play" ? "플레이리스트를 재생합니다." : "재생목록 끝에 추가했습니다.");
    } catch (caught) {
      onNotice(errorMessage(caught), true);
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
        <div><p className="playlist-eyebrow">내 컬렉션</p><h1 id="playlists-heading" className="page-heading">저장된 플레이리스트</h1><p className="muted">재생 대기열과 별도로 오래 보관하는 곡 목록입니다.</p></div>
      </header>

      <div className="playlist-layout">
        <aside className="playlist-sidebar" aria-label="저장된 플레이리스트">
          <form className="playlist-create" onSubmit={(event) => { event.preventDefault(); void createPlaylist(); }}>
            <label htmlFor="playlist-new-name">새 플레이리스트</label>
            <div><input id="playlist-new-name" className="input" value={newName} onChange={(event) => setNewName(event.target.value)} maxLength={120} placeholder="이름" /><button className="button button-primary" type="submit" disabled={busy || !newName.trim()} aria-label="새 플레이리스트 만들기"><Icon name="plus" /> 만들기</button></div>
            {createError && <p className="error-text" role="alert">{createError}</p>}
          </form>

          {loadingList && <p className="playlist-loading" role="status">목록을 불러오는 중…</p>}
          {listError && <div className="playlist-inline-error" role="alert"><p className="error-text">{listError}</p><button className="button button-ghost" type="button" onClick={() => setReload((current) => current + 1)}>다시 시도</button></div>}
          {!loadingList && !listError && playlists.length === 0 && <div className="empty-state">저장된 플레이리스트가 없습니다.</div>}
          <div className="playlist-list">
            {playlists.map((playlist) => (
              <button className={`playlist-list-item${selectedID === playlist.id ? " playlist-list-item-active" : ""}`} type="button" key={playlist.id} onClick={() => selectPlaylist(playlist.id)} aria-current={selectedID === playlist.id ? "page" : undefined}>
                <span className="playlist-list-icon"><Icon name="playlist" /></span><span><strong>{playlist.name}</strong><small>{playlist.track_ids?.length ?? playlist.tracks?.length ?? 0}곡</small></span>
              </button>
            ))}
          </div>
        </aside>

        <main className="playlist-editor">
          {!selectedID && !loadingList && <div className="empty-state">왼쪽에서 플레이리스트를 만들거나 선택해 주세요.</div>}
          {selectedID && !loadingDetail && !baseline && detailError && <div className="playlist-inline-error" role="alert"><p className="error-text">{detailError}</p><button className="button button-ghost" type="button" onClick={() => setReload((current) => current + 1)}>다시 시도</button></div>}
          {selectedID && loadingDetail && !baseline && <p className="playlist-loading" role="status">플레이리스트를 불러오는 중…</p>}
          {baseline && (
            <>
              <header className="playlist-editor-heading">
                <div className="playlist-title-field"><label htmlFor="playlist-title">플레이리스트 이름</label><input id="playlist-title" className="input" value={draftName} onChange={(event) => setDraftName(event.target.value)} maxLength={120} /></div>
                <p className="playlist-summary">{entries.length}곡 · 재생 가능 {availableCount}곡 · {formatDuration(totalDuration)}</p>
                <div className="playlist-primary-actions">
                  <button className="button button-primary" type="button" disabled={busy || !availableCount} onClick={() => queuePlaylist("play")}><Icon name="play" /> 재생</button>
                  <button className="button button-ghost" type="button" disabled={busy || !availableCount} onClick={() => queuePlaylist("append")}><Icon name="append" /> 끝에 추가</button>
                  <button className="button button-ghost" type="button" disabled={busy || !dirty || Boolean(pendingRemote) || !draftName.trim()} onClick={savePlaylist}><Icon name="save" /> 변경 저장</button>
                  <button className="button button-ghost" type="button" disabled={busy || (!dirty && !pendingRemote)} onClick={discardDraft}>변경 취소</button>
                </div>
              </header>

              {pendingRemote && (
                <div className="playlist-conflict" role="alert">
                  <div><strong>다른 곳에서 변경됨</strong><p>서버의 최신 변경을 불러오거나, 현재 편집 내용을 최신 버전에 다시 적용할 수 있습니다. 자동으로 덮어쓰지 않습니다.</p></div>
                  <div><button className="button button-ghost" type="button" onClick={loadRemoteVersion}>서버 변경 불러오기</button><button className="button button-primary" type="button" onClick={keepDraftOnLatestRevision}>내 편집 다시 적용</button></div>
                </div>
              )}
              {detailError && !pendingRemote && <p className="error-text playlist-detail-error" role="alert">{detailError}</p>}

              {entries.length === 0 ? <div className="empty-state">아직 곡이 없습니다. 라이브러리에서 이 플레이리스트에 곡을 추가해 주세요.</div> : (
                <div className="playlist-entry-list" role="list" aria-label={`${draftName || baseline.name} 수록곡`}>
                  {entries.map((entry, index) => {
                    const track = entry.track;
                    return (
                      <article className={`playlist-entry${track?.available === false ? " playlist-entry-unavailable" : ""}`} role="listitem" key={entry.key}>
                        <span className="playlist-entry-number">{index + 1}</span>
                        <div className="playlist-entry-copy"><strong>{track?.title ?? "곡 정보를 불러올 수 없음"}</strong><span>{track ? `${track.artist || "아티스트 정보 없음"}${track.album ? ` · ${track.album}` : ""}` : entry.trackID}</span>{track?.available === false && <small>원본 파일을 현재 사용할 수 없음</small>}</div>
                        <time className="playlist-entry-duration">{formatDuration(track?.duration_ms ?? 0)}</time>
                        <div className="playlist-entry-actions" aria-label={`${track?.title ?? "곡"} 순서와 항목 편집`}>
                          <button type="button" disabled={busy || index === 0} onClick={() => moveEntry(index, -1)} aria-label={`${track?.title ?? "곡"} 위로 이동`} title="위로 이동"><Icon name="up" /></button>
                          <button type="button" disabled={busy || index === entries.length - 1} onClick={() => moveEntry(index, 1)} aria-label={`${track?.title ?? "곡"} 아래로 이동`} title="아래로 이동"><Icon name="down" /></button>
                          <button className="playlist-remove-button" type="button" disabled={busy} onClick={() => setEntries((current) => current.filter((_, currentIndex) => currentIndex !== index))} aria-label={`${track?.title ?? "곡"} 플레이리스트에서 제거`} title="플레이리스트에서 제거"><Icon name="remove" /></button>
                        </div>
                      </article>
                    );
                  })}
                </div>
              )}

              <footer className="playlist-danger-zone">
                <div><strong>플레이리스트 삭제</strong><p>저장된 목록만 삭제하며 원본 음악 파일은 삭제하지 않습니다.</p></div>
                {!deleteArmed ? <button className="button button-ghost playlist-danger-button" type="button" disabled={busy} onClick={() => setDeleteArmed(true)}><Icon name="trash" /> 삭제</button> : <div className="playlist-delete-confirm"><span>정말 삭제할까요?</span><button className="button button-ghost" type="button" onClick={() => setDeleteArmed(false)}>취소</button><button className="button playlist-danger-button" type="button" disabled={busy} onClick={deletePlaylist}>목록 삭제</button></div>}
              </footer>
            </>
          )}
        </main>
      </div>
    </section>
  );
}
