import { useEffect, useMemo, useRef, useState } from "react";
import { addTracks, api, playTracks } from "./api";
import type { Album, Artist, Folder, Genre, Page, Playlist, Track } from "./types";
import "./library.css";

type Props = {
  revision: number;
  onNotice: (message: string, error?: boolean) => void;
  onQueueChange: () => void;
};

type LibraryKind = "albums" | "artists" | "genres" | "folders" | "tracks";
type BrowseItem = Album | Artist | Genre | Folder | Track;
type Scope =
  | { kind: "album"; id: string; title: string; subtitle: string; artworkID: string }
  | { kind: "artist"; name: string; title: string }
  | { kind: "genre"; name: string; title: string }
  | { kind: "folder"; rootID: string; path: string; title: string };
type QueueAction = "play" | "next" | "append";

const tabs: Array<{ kind: LibraryKind; label: string }> = [
  { kind: "albums", label: "앨범" },
  { kind: "artists", label: "아티스트" },
  { kind: "genres", label: "장르" },
  { kind: "folders", label: "폴더" },
  { kind: "tracks", label: "곡" },
];

const pageSize = 48;
const trackPageSize = 100;

function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function errorMessage(error: unknown): string {
  if (error && typeof error === "object" && "message" in error) {
    return String(error.message);
  }
  return "요청을 완료하지 못했습니다.";
}

function isConflict(error: unknown): boolean {
  return Boolean(error && typeof error === "object" && "status" in error && error.status === 409);
}

function artworkURL(id: string): string | undefined {
  return id ? `/api/v1/artwork/${encodeURIComponent(id)}` : undefined;
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

function Icon({ name }: { name: "music" | "play" | "next" | "append" | "playlist" | "folder" | "back" | "search" }) {
  const paths = {
    music: <><path d="M9 18V5l11-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="17" cy="16" r="3" /></>,
    play: <path d="m8 5 11 7-11 7z" />,
    next: <><path d="m5 5 10 7L5 19z" /><path d="M19 5v14" /></>,
    append: <><path d="M4 6h10M4 12h10M4 18h7" /><path d="M18 14v7m-3.5-3.5h7" /></>,
    playlist: <><path d="M4 6h12M4 11h12M4 16h7" /><path d="M18 14v7m-3.5-3.5h7" /></>,
    folder: <path d="M3 6.5h7l2 2h9v10H3z" />,
    back: <path d="m15 18-6-6 6-6" />,
    search: <><circle cx="11" cy="11" r="7" /><path d="m20 20-4-4" /></>,
  };
  return <svg className="library-icon" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{paths[name]}</svg>;
}

function Artwork({ id, alt, compact = false }: { id: string; alt: string; compact?: boolean }) {
  const [failedID, setFailedID] = useState("");
  const src = artworkURL(id);
  if (!src || failedID === id) {
    return <span className={`library-artwork library-artwork-empty${compact ? " library-artwork-compact" : ""}`} role="img" aria-label={`${alt} 아트워크 없음`}><Icon name="music" /></span>;
  }
  return <img className={`library-artwork${compact ? " library-artwork-compact" : ""}`} src={src} alt={`${alt} 아트워크`} loading="lazy" onError={() => setFailedID(id)} />;
}

function paramsFor(kind: LibraryKind | "tracks", search: string, offset: number, limit: number, scope: Scope | null): URLSearchParams {
  const params = new URLSearchParams({ offset: String(offset), limit: String(limit) });
  if (search.trim()) params.set("q", search.trim());
  if (scope?.kind === "album") {
    params.set("album_id", scope.id);
    params.set("sort", "album");
  } else if (scope?.kind === "artist") {
    params.set("artist", scope.name);
    params.set("sort", "album");
  } else if (scope?.kind === "genre") {
    params.set("genre", scope.name);
    params.set("sort", "album");
  } else if (scope?.kind === "folder") {
    params.set("root_id", scope.rootID);
    params.set("path", scope.path);
    params.set("sort", "path");
  } else if (kind === "albums") {
    params.set("sort", "album");
  } else if (kind === "artists") {
    params.set("sort", "artist");
  } else if (kind === "tracks") {
    params.set("sort", "title");
  }
  return params;
}

export default function Library({ revision, onNotice, onQueueChange }: Props) {
  const [kind, setKind] = useState<LibraryKind>("albums");
  const [scope, setScope] = useState<Scope | null>(null);
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [offset, setOffset] = useState(0);
  const [page, setPage] = useState<Page<BrowseItem> | null>(null);
  const [childFolders, setChildFolders] = useState<Folder[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);
  const [actionBusy, setActionBusy] = useState(false);
  const [playlists, setPlaylists] = useState<Playlist[]>([]);
  const [pickerTracks, setPickerTracks] = useState<Track[] | null>(null);
  const [pickerTarget, setPickerTarget] = useState("");
  const [newPlaylistName, setNewPlaylistName] = useState("");
  const [pickerError, setPickerError] = useState("");
  const requestSerial = useRef(0);

  useEffect(() => {
    const timer = window.setTimeout(() => setSearch(searchInput), 250);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  useEffect(() => {
    setOffset(0);
  }, [kind, scope, search]);

  useEffect(() => {
    const controller = new AbortController();
    const serial = ++requestSerial.current;
    const effectiveKind: LibraryKind = scope ? "tracks" : kind;
    const limit = effectiveKind === "tracks" ? trackPageSize : pageSize;
    const params = paramsFor(effectiveKind, search, offset, limit, scope);
    setLoading(true);
    setError("");

    const pageRequest = api<Page<BrowseItem>>(`/library/${effectiveKind}?${params}`, { signal: controller.signal });
    const foldersRequest: Promise<Page<Folder> | null> = scope?.kind === "folder"
      ? api<Page<Folder>>(`/library/folders?${new URLSearchParams({ root_id: scope.rootID, path: scope.path, offset: "0", limit: "200", sort: "path" })}`, { signal: controller.signal })
      : Promise.resolve(null);

    Promise.all([pageRequest, foldersRequest])
      .then(([nextPage, folders]) => {
        if (requestSerial.current !== serial) return;
        setPage(nextPage);
        if (scope?.kind === "folder" && folders) {
          setChildFolders(folders.items.filter((folder) => folder.root_id === scope.rootID && folder.path !== scope.path));
        } else {
          setChildFolders([]);
        }
      })
      .catch((caught: unknown) => {
        if (isAbort(caught) || requestSerial.current !== serial) return;
        setPage(null);
        setChildFolders([]);
        setError(errorMessage(caught));
      })
      .finally(() => {
        if (requestSerial.current === serial) setLoading(false);
      });

    return () => controller.abort();
  }, [kind, offset, reload, revision, scope, search]);

  useEffect(() => {
    const controller = new AbortController();
    api<{ items: Playlist[] }>("/playlists", { signal: controller.signal })
      .then((result) => {
        if (!controller.signal.aborted) setPlaylists(result.items);
      })
      .catch((caught: unknown) => {
        if (!controller.signal.aborted && !isAbort(caught)) setPlaylists([]);
      });
    return () => controller.abort();
  }, [revision]);
  useEffect(() => {
    if (!pickerTracks) return;
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !actionBusy) setPickerTracks(null);
    };
    document.addEventListener("keydown", closeOnEscape);
    return () => document.removeEventListener("keydown", closeOnEscape);
  }, [actionBusy, pickerTracks]);
  useEffect(() => {
    if (pickerTracks && !playlists.some((playlist) => playlist.id === pickerTarget)) {
      setPickerTarget(playlists[0]?.id ?? "");
    }
  }, [pickerTarget, pickerTracks, playlists]);



  const visibleTracks = useMemo(
    () => (page?.items ?? []).filter((item): item is Track => "duration_ms" in item),
    [page],
  );
  const playableVisibleTracks = visibleTracks.filter((track) => track.available);

  async function collectScopeTracks(): Promise<Track[]> {
    if (!scope) return playableVisibleTracks;
    const collected: Track[] = [];
    let nextOffset = 0;
    let total = 1;
    while (nextOffset < total) {
      const params = paramsFor("tracks", search, nextOffset, 200, scope);
      const result = await api<Page<Track>>(`/library/tracks?${params}`);
      collected.push(...result.items.filter((track) => track.available));
      total = result.total;
      if (!result.items.length) break;
      nextOffset += result.items.length;
    }
    return collected;
  }

  async function runQueue(tracks: Track[], action: QueueAction) {
    const trackIDs = tracks.filter((track) => track.available).map((track) => track.id);
    if (!trackIDs.length) {
      onNotice("재생 가능한 곡이 없습니다.", true);
      return;
    }
    setActionBusy(true);
    try {
      if (action === "play") await playTracks(trackIDs);
      else await addTracks(trackIDs, action);
      onQueueChange();
      onNotice(action === "play" ? "재생을 시작했습니다." : action === "next" ? "다음 재생에 추가했습니다." : "재생목록 끝에 추가했습니다.");
    } catch (caught) {
      onNotice(errorMessage(caught), true);
    } finally {
      setActionBusy(false);
    }
  }

  async function runScopeQueue(action: QueueAction) {
    setActionBusy(true);
    try {
      const tracks = await collectScopeTracks();
      const trackIDs = tracks.map((track) => track.id);
      if (!trackIDs.length) throw new Error("재생 가능한 곡이 없습니다.");
      if (action === "play") await playTracks(trackIDs);
      else await addTracks(trackIDs, action);
      onQueueChange();
      onNotice(action === "play" ? "선택한 음악을 재생합니다." : action === "next" ? "다음 재생에 추가했습니다." : "재생목록 끝에 추가했습니다.");
    } catch (caught) {
      onNotice(errorMessage(caught), true);
    } finally {
      setActionBusy(false);
    }
  }

  async function openScopePicker() {
    setActionBusy(true);
    try {
      const tracks = await collectScopeTracks();
      if (!tracks.length) throw new Error("추가할 수 있는 곡이 없습니다.");
      setPickerTracks(tracks);
      setPickerTarget(playlists[0]?.id ?? "");
      setPickerError("");
      setNewPlaylistName("");
    } catch (caught) {
      onNotice(errorMessage(caught), true);
    } finally {
      setActionBusy(false);
    }
  }

  function openTrackPicker(track: Track) {
    if (!track.available) return;
    setPickerTracks([track]);
    setPickerTarget(playlists[0]?.id ?? "");
    setPickerError("");
    setNewPlaylistName("");
  }

  async function addToPlaylist() {
    if (!pickerTracks?.length || !pickerTarget) return;
    setActionBusy(true);
    setPickerError("");
    try {
      const current = await api<Playlist>(`/playlists/${encodeURIComponent(pickerTarget)}`);
      const trackIDs = [...current.track_ids, ...pickerTracks.map((track) => track.id)];
      await api<Playlist>(`/playlists/${encodeURIComponent(current.id)}`, {
        method: "PUT",
        body: JSON.stringify({ name: current.name, track_ids: trackIDs, revision: current.revision }),
      });
      setPickerTracks(null);
      onNotice(`‘${current.name}’에 추가했습니다.`);
    } catch (caught) {
      if (isConflict(caught)) {
        setPickerError("다른 곳에서 플레이리스트가 변경되었습니다. 최신 목록을 확인한 뒤 다시 선택해 주세요.");
        try {
          const result = await api<{ items: Playlist[] }>("/playlists");
          setPlaylists(result.items);
        } catch {
          // The original conflict is the useful, safe error to show.
        }
      } else {
        setPickerError(errorMessage(caught));
      }
    } finally {
      setActionBusy(false);
    }
  }

  async function createPlaylistWithTracks() {
    const name = newPlaylistName.trim();
    if (!name || !pickerTracks?.length) return;
    setActionBusy(true);
    setPickerError("");
    try {
      const created = await api<Playlist>("/playlists", {
        method: "POST",
        body: JSON.stringify({ name, track_ids: pickerTracks.map((track) => track.id) }),
      });
      setPlaylists((current) => [...current, created]);
      setNewPlaylistName("");
      setPickerTracks(null);
      onNotice(`‘${created.name}’ 플레이리스트를 만들었습니다.`);
    } catch (caught) {
      setPickerError(errorMessage(caught));
    } finally {
      setActionBusy(false);
    }
  }

  function openItem(item: BrowseItem) {
    if (kind === "albums" && "artwork_id" in item && "track_count" in item && "title" in item) {
      const album = item as Album;
      setScope({ kind: "album", id: album.id, title: album.title, subtitle: album.artist, artworkID: album.artwork_id });
    } else if (kind === "artists" && "name" in item) {
      const artist = item as Artist;
      setScope({ kind: "artist", name: artist.name, title: artist.name });
    } else if (kind === "genres" && "name" in item) {
      const genre = item as Genre;
      setScope({ kind: "genre", name: genre.name, title: genre.name });
    } else if (kind === "folders" && "root_id" in item) {
      const folder = item as Folder;
      setScope({ kind: "folder", rootID: folder.root_id, path: folder.path, title: folder.name });
    }
  }

  function leaveScope() {
    setScope(null);
    setChildFolders([]);
  }

  const total = page?.total ?? 0;
  const limit = page?.limit || (scope || kind === "tracks" ? trackPageSize : pageSize);
  const start = total ? offset + 1 : 0;
  const end = Math.min(offset + (page?.items.length ?? 0), total);

  return (
    <section className="library-shell" aria-labelledby="library-heading">
      <header className="library-heading-row">
        <div>
          <p className="library-eyebrow">내 음악</p>
          <h1 id="library-heading" className="page-heading">음악 라이브러리</h1>
        </div>
        <label className="library-search">
          <span className="library-search-icon"><Icon name="search" /></span>
          <span className="library-visually-hidden">라이브러리 검색</span>
          <input className="input" type="search" value={searchInput} onChange={(event) => setSearchInput(event.target.value)} placeholder="제목, 아티스트, 앨범 검색" autoComplete="off" />
        </label>
      </header>

      <nav className="library-tabs" aria-label="라이브러리 분류">
        {tabs.map((tab) => (
          <button key={tab.kind} className={`library-tab${kind === tab.kind && !scope ? " library-tab-active" : ""}`} type="button" onClick={() => { setKind(tab.kind); setScope(null); }} aria-current={kind === tab.kind && !scope ? "page" : undefined}>{tab.label}</button>
        ))}
      </nav>

      {scope && (
        <div className="library-detail-header">
          <button className="button button-ghost" type="button" onClick={leaveScope}><Icon name="back" /> 목록으로</button>
          <div className="library-detail-summary">
            {scope.kind === "album" ? <Artwork id={scope.artworkID} alt={scope.title} /> : <span className="library-detail-symbol" aria-hidden="true"><Icon name={scope.kind === "folder" ? "folder" : "music"} /></span>}
            <div className="library-detail-copy">
              <span className="muted">{scope.kind === "album" ? "앨범" : scope.kind === "artist" ? "아티스트" : scope.kind === "genre" ? "장르" : "폴더"}</span>
              <h2>{scope.title}</h2>
              {scope.kind === "album" && <p>{scope.subtitle}</p>}
              {scope.kind === "folder" && <p className="library-path">{scope.path}</p>}
            </div>
          </div>
          <div className="library-bulk-actions" aria-label="현재 목록 작업">
            <button className="button button-primary" type="button" disabled={actionBusy || (!loading && !page?.total)} onClick={() => runScopeQueue("play")}><Icon name="play" /> 전체 재생</button>
            <button className="button button-ghost" type="button" disabled={actionBusy || (!loading && !page?.total)} onClick={() => runScopeQueue("next")}><Icon name="next" /> 다음에</button>
            <button className="button button-ghost" type="button" disabled={actionBusy || (!loading && !page?.total)} onClick={() => runScopeQueue("append")}><Icon name="append" /> 끝에 추가</button>
            <button className="button button-ghost" type="button" disabled={actionBusy || (!loading && !page?.total)} onClick={openScopePicker}><Icon name="playlist" /> 저장 목록에</button>
          </div>
        </div>
      )}

      {error && <div className="library-error" role="alert"><p className="error-text">{error}</p><button className="button button-ghost" type="button" onClick={() => setReload((current) => current + 1)}>다시 시도</button></div>}
      {loading && <div className="library-loading" role="status">라이브러리를 불러오는 중…</div>}

      {!loading && !error && page && page.items.length === 0 && childFolders.length === 0 && (
        <div className="empty-state">{search ? "검색 결과가 없습니다." : "표시할 음악이 없습니다. 설정에서 음악 폴더를 확인하고 라이브러리를 스캔해 주세요."}</div>
      )}

      {!loading && !error && !scope && kind === "albums" && (
        <div className="library-album-grid">
          {(page?.items as Album[] | undefined)?.map((album) => (
            <button className="library-album-card" type="button" key={album.id} onClick={() => openItem(album)}>
              <Artwork id={album.artwork_id} alt={album.title} />
              <span className="library-card-title">{album.title}</span>
              <span className="library-card-meta">{album.artist || "아티스트 정보 없음"} · {album.track_count}곡</span>
            </button>
          ))}
        </div>
      )}

      {!loading && !error && !scope && kind === "artists" && (
        <div className="library-name-grid">
          {(page?.items as Artist[] | undefined)?.map((artist) => <button className="library-name-card" type="button" key={artist.id} onClick={() => openItem(artist)}><span className="library-name-mark"><Icon name="music" /></span><span><strong>{artist.name}</strong><small>{artist.track_count}곡</small></span></button>)}
        </div>
      )}

      {!loading && !error && !scope && kind === "genres" && (
        <div className="library-name-grid">
          {(page?.items as Genre[] | undefined)?.map((genre) => <button className="library-name-card" type="button" key={genre.id} onClick={() => openItem(genre)}><span className="library-name-mark"><Icon name="music" /></span><span><strong>{genre.name}</strong><small>{genre.track_count}곡</small></span></button>)}
        </div>
      )}

      {!loading && !error && !scope && kind === "folders" && (
        <div className="library-folder-list">
          {(page?.items as Folder[] | undefined)?.map((folder) => <button className="library-folder-row" type="button" key={`${folder.root_id}:${folder.path}`} onClick={() => openItem(folder)}><span className="library-folder-icon"><Icon name="folder" /></span><span><strong>{folder.name}</strong><small>{folder.path || "최상위 폴더"}</small></span><span className="library-folder-count">{folder.track_count}곡</span></button>)}
        </div>
      )}

      {!loading && !error && scope?.kind === "folder" && childFolders.length > 0 && (
        <div className="library-child-folders" aria-label="하위 폴더">
          {childFolders.map((folder) => <button className="library-folder-row" type="button" key={`${folder.root_id}:${folder.path}`} onClick={() => setScope({ kind: "folder", rootID: folder.root_id, path: folder.path, title: folder.name })}><span className="library-folder-icon"><Icon name="folder" /></span><span><strong>{folder.name}</strong><small>{folder.path}</small></span><span className="library-folder-count">{folder.track_count}곡</span></button>)}
        </div>
      )}

      {!loading && !error && (scope || kind === "tracks") && visibleTracks.length > 0 && (
        <div className="library-track-list" role="list" aria-label="곡 목록">
          {visibleTracks.map((track, index) => (
            <article className={`library-track-row${track.available ? "" : " library-track-unavailable"}`} role="listitem" key={track.id}>
              <span className="library-track-number">{scope?.kind === "album" ? `${track.disc > 1 ? `${track.disc}-` : ""}${track.track || offset + index + 1}` : offset + index + 1}</span>
              <Artwork id={track.artwork_id} alt={track.album || track.title} compact />
              <div className="library-track-main"><strong>{track.title}</strong><span>{track.artist || "아티스트 정보 없음"}</span></div>
              <div className="library-track-album"><span>{track.album || "앨범 정보 없음"}</span>{!track.available && <small>파일을 사용할 수 없음</small>}</div>
              <time className="library-track-duration">{formatDuration(track.duration_ms)}</time>
              <div className="library-track-actions" aria-label={`${track.title} 작업`}>
                <button type="button" title="지금 재생" aria-label={`${track.title} 지금 재생`} disabled={!track.available || actionBusy} onClick={() => runQueue([track], "play")}><Icon name="play" /></button>
                <button type="button" title="다음에 재생" aria-label={`${track.title} 다음에 재생`} disabled={!track.available || actionBusy} onClick={() => runQueue([track], "next")}><Icon name="next" /></button>
                <button type="button" title="끝에 추가" aria-label={`${track.title} 재생목록 끝에 추가`} disabled={!track.available || actionBusy} onClick={() => runQueue([track], "append")}><Icon name="append" /></button>
                <button type="button" title="저장 목록에 추가" aria-label={`${track.title} 저장된 플레이리스트에 추가`} disabled={!track.available || actionBusy} onClick={() => openTrackPicker(track)}><Icon name="playlist" /></button>
              </div>
            </article>
          ))}
        </div>
      )}

      {!loading && !error && total > limit && (
        <nav className="library-pagination" aria-label="페이지 이동">
          <button className="button button-ghost" type="button" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>이전</button>
          <span>{start.toLocaleString("ko-KR")}–{end.toLocaleString("ko-KR")} / {total.toLocaleString("ko-KR")}</span>
          <button className="button button-ghost" type="button" disabled={offset + limit >= total} onClick={() => setOffset(offset + limit)}>다음</button>
        </nav>
      )}

      {pickerTracks && (
        <div className="library-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.currentTarget === event.target) setPickerTracks(null); }}>
          <section className="library-dialog" role="dialog" aria-modal="true" aria-labelledby="library-picker-title">
            <div className="library-dialog-heading"><div><p className="library-eyebrow">저장된 플레이리스트</p><h2 id="library-picker-title">{pickerTracks.length}곡 추가</h2></div><button className="button button-ghost" type="button" onClick={() => setPickerTracks(null)}>닫기</button></div>
            {playlists.length > 0 && <><label className="library-field"><span>추가할 플레이리스트</span><select className="input" value={pickerTarget} onChange={(event) => setPickerTarget(event.target.value)} autoFocus>{playlists.map((playlist) => <option key={playlist.id} value={playlist.id}>{playlist.name}</option>)}</select></label><button className="button button-primary library-dialog-submit" type="button" disabled={actionBusy || !pickerTarget} onClick={addToPlaylist}>선택한 목록에 추가</button></>}
            <div className="library-dialog-divider"><span>또는 새로 만들기</span></div>
            <form className="library-create-inline" onSubmit={(event) => { event.preventDefault(); void createPlaylistWithTracks(); }}><label className="library-field"><span>새 플레이리스트 이름</span><input className="input" value={newPlaylistName} onChange={(event) => setNewPlaylistName(event.target.value)} maxLength={120} autoFocus={playlists.length === 0} /></label><button className="button button-ghost" type="submit" disabled={actionBusy || !newPlaylistName.trim()}>만들고 추가</button></form>
            {pickerError && <p className="error-text" role="alert">{pickerError}</p>}
          </section>
        </div>
      )}
    </section>
  );
}
