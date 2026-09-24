import { useEffect, useMemo, useRef, useState } from "react";
import { addTracks, api, playTracks } from "./api";
import type { Album, Artist, Folder, Genre, Page, Playlist, Track } from "./types";
import TrackInfoDialog from "./TrackInfoDialog";
import { useI18n, type MessageKey } from "./i18n";
import { NativeDownloadAction, type JastreamerDownloads } from "./JastreamerDownloads";
import "./library.css";

type Props = {
  revision: number;
  onNotice: (message: string, error?: boolean) => void;
  onQueueChange: () => void;
  downloads: JastreamerDownloads;
};

type LibraryKind = "albums" | "artists" | "genres" | "folders" | "tracks" | "most_played";
type BrowseItem = Album | Artist | Genre | Folder | Track;
type Scope =
  | { kind: "album"; id: string; title: string; subtitle: string; artworkID: string }
  | { kind: "artist"; name: string; title: string }
  | { kind: "genre"; name: string; title: string }
  | { kind: "folder"; rootID: string; rootTitle: string; path: string; title: string };
type QueueAction = "play" | "next" | "append";

const tabs: Array<{ kind: LibraryKind; labelKey: MessageKey }> = [
  { kind: "albums", labelKey: "library.tabs.albums" },
  { kind: "artists", labelKey: "library.tabs.artists" },
  { kind: "genres", labelKey: "library.tabs.genres" },
  { kind: "folders", labelKey: "library.tabs.folders" },
  { kind: "tracks", labelKey: "library.tabs.tracks" },
  { kind: "most_played", labelKey: "library.tabs.mostPlayed" },
];

const scopeTypeKeys: Record<Scope["kind"], MessageKey> = {
  album: "library.type.album",
  artist: "library.type.artist",
  genre: "library.type.genre",
  folder: "library.type.folder",
};

const pageSize = 48;
const trackPageSize = 100;

function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function errorMessage(error: unknown, fallback: string): string {
  if (error && typeof error === "object" && "message" in error) {
    return String(error.message);
  }
  return fallback;
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

function Icon({ name }: { name: "music" | "play" | "next" | "append" | "playlist" | "folder" | "back" | "search" | "info" | "heart" }) {
  const paths = {
    music: <><path d="M9 18V5l11-2v13" /><circle cx="6" cy="18" r="3" /><circle cx="17" cy="16" r="3" /></>,
    play: <path d="m8 5 11 7-11 7z" />,
    next: <><path d="m5 5 10 7L5 19z" /><path d="M19 5v14" /></>,
    append: <><path d="M4 6h9M4 12h7M4 18h9" /><path d="M18 4v14m-3-3 3 3 3-3" /></>,
    playlist: <><path d="M6 3h12v18l-6-3.5L6 21z" /><path d="M12 7v6M9 10h6" /></>,
    folder: <path d="M3 6.5h7l2 2h9v10H3z" />,
    back: <path d="m15 18-6-6 6-6" />,
    search: <><circle cx="11" cy="11" r="7" /><path d="m20 20-4-4" /></>,
    info: <><circle cx="12" cy="12" r="9" /><path d="M12 11v6" /><path d="M12 7h.01" /></>,
    heart: <path d="M20.8 5.8a5.5 5.5 0 0 0-7.8 0L12 6.9l-1.1-1.1a5.5 5.5 0 0 0-7.8 7.8L12 22l8.8-8.4a5.5 5.5 0 0 0 0-7.8z" />,
  };
  return <svg className="library-icon" viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{paths[name]}</svg>;
}

function parentFolderPath(path: string): string {
  const slash = path.lastIndexOf("/");
  return slash < 0 ? "" : path.slice(0, slash);
}

function Artwork({ id, label, missingLabel, compact = false }: { id: string; label: string; missingLabel: string; compact?: boolean }) {
  const [failedID, setFailedID] = useState("");
  const src = artworkURL(id);
  if (!src || failedID === id) {
    return <span className={`library-artwork library-artwork-empty${compact ? " library-artwork-compact" : ""}`} role="img" aria-label={missingLabel}><Icon name="music" /></span>;
  }
  return <img className={`library-artwork${compact ? " library-artwork-compact" : ""}`} src={src} alt={label} loading="lazy" onError={() => setFailedID(id)} />;
}

function paramsFor(kind: LibraryKind, search: string, offset: number, limit: number, scope: Scope | null, likedOnly = false): URLSearchParams {
  const params = new URLSearchParams({ offset: String(offset), limit: String(limit) });
  if (search.trim()) params.set("q", search.trim());
  if (likedOnly) params.set("liked", "true");
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
  } else if (kind === "most_played") {
    params.set("played", "true");
    params.set("sort", "most_played");
  } else if (kind === "tracks") {
    params.set("sort", "title");
  }
  return params;
}

export default function Library({ revision, onNotice, onQueueChange, downloads }: Props) {
  const { locale, t } = useI18n();
  const numberFormatter = useMemo(() => new Intl.NumberFormat(locale), [locale]);
  const [kind, setKind] = useState<LibraryKind>("albums");
  const [scope, setScope] = useState<Scope | null>(null);
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [likedOnly, setLikedOnly] = useState(false);
  const [offset, setOffset] = useState(0);
  const [page, setPage] = useState<Page<BrowseItem> | null>(null);
  const [childFolders, setChildFolders] = useState<Folder[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);
  const [actionBusy, setActionBusy] = useState(false);
  const [likeBusy, setLikeBusy] = useState<Set<string>>(() => new Set());
  const [playlists, setPlaylists] = useState<Playlist[]>([]);
  const [pickerTracks, setPickerTracks] = useState<Track[] | null>(null);
  const [pickerTarget, setPickerTarget] = useState("");
  const [newPlaylistName, setNewPlaylistName] = useState("");
  const [pickerError, setPickerError] = useState("");
  const [infoTrackID, setInfoTrackID] = useState<string | null>(null);
  const requestSerial = useRef(0);

  useEffect(() => {
    const timer = window.setTimeout(() => setSearch(searchInput), 250);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  useEffect(() => {
    setOffset(0);
  }, [kind, likedOnly, scope, search]);

  useEffect(() => {
    const controller = new AbortController();
    const serial = ++requestSerial.current;
    const endpointKind: Exclude<LibraryKind, "most_played"> = scope || kind === "most_played" ? "tracks" : kind;
    const limit = endpointKind === "tracks" ? trackPageSize : pageSize;
    const params = paramsFor(kind, search, offset, limit, scope, likedOnly);
    setLoading(true);
    setError("");

    const pageRequest = api<Page<BrowseItem>>(`/library/${endpointKind}?${params}`, { signal: controller.signal });
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
        setError(errorMessage(caught, t("common.requestFailed")));
      })
      .finally(() => {
        if (requestSerial.current === serial) setLoading(false);
      });

    return () => controller.abort();
  }, [kind, likedOnly, offset, reload, revision, scope, search, t]);

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
      onNotice(t("library.noPlayableTracks"), true);
      return;
    }
    setActionBusy(true);
    try {
      if (action === "play") await playTracks(trackIDs);
      else await addTracks(trackIDs, action);
      onQueueChange();
      onNotice(t(action === "play" ? "library.playStarted" : action === "next" ? "library.addedNext" : "library.addedEnd"));
    } catch (caught) {
      onNotice(errorMessage(caught, t("common.requestFailed")), true);
    } finally {
      setActionBusy(false);
    }
  }

  async function runScopeQueue(action: QueueAction) {
    setActionBusy(true);
    try {
      const tracks = await collectScopeTracks();
      const trackIDs = tracks.map((track) => track.id);
      if (!trackIDs.length) {
        onNotice(t("library.noPlayableTracks"), true);
        return;
      }
      if (action === "play") await playTracks(trackIDs);
      else await addTracks(trackIDs, action);
      onQueueChange();
      onNotice(t(action === "play" ? "library.selectionPlaying" : action === "next" ? "library.addedNext" : "library.addedEnd"));
    } catch (caught) {
      onNotice(errorMessage(caught, t("common.requestFailed")), true);
    } finally {
      setActionBusy(false);
    }
  }

  async function openScopePicker() {
    setActionBusy(true);
    try {
      const tracks = await collectScopeTracks();
      if (!tracks.length) {
        onNotice(t("library.noTracksToAdd"), true);
        return;
      }
      setPickerTracks(tracks);
      setPickerTarget(playlists[0]?.id ?? "");
      setPickerError("");
      setNewPlaylistName("");
    } catch (caught) {
      onNotice(errorMessage(caught, t("common.requestFailed")), true);
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
      onNotice(t("library.addedToPlaylist", { name: current.name }));
    } catch (caught) {
      if (isConflict(caught)) {
        setPickerError(t("library.playlistConflict"));
        try {
          const result = await api<{ items: Playlist[] }>("/playlists");
          setPlaylists(result.items);
        } catch {
          // The original conflict is the useful, safe error to show.
        }
      } else {
        setPickerError(errorMessage(caught, t("common.requestFailed")));
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
      onNotice(t("library.playlistCreated", { name: created.name }));
    } catch (caught) {
      setPickerError(errorMessage(caught, t("common.requestFailed")));
    } finally {
      setActionBusy(false);
    }
  }
  async function toggleLiked(track: Track) {
    if (likeBusy.has(track.id)) return;
    const serial = requestSerial.current;
    const liked = !track.liked;
    setLikeBusy((current) => new Set(current).add(track.id));
    setPage((current) => current ? {
      ...current,
      items: current.items.map((item) => "duration_ms" in item && item.id === track.id ? { ...item, liked } : item),
    } : current);
    try {
      const updated = await api<Track>(`/library/tracks/${encodeURIComponent(track.id)}/like`, {
        method: "PUT",
        body: JSON.stringify({ liked }),
      });
      setPage((current) => {
        if (!current || requestSerial.current !== serial) return current;
        if (likedOnly && !updated.liked) {
          const items = current.items.filter((item) => !("duration_ms" in item) || item.id !== updated.id);
          return { ...current, items, total: Math.max(0, current.total - (items.length === current.items.length ? 0 : 1)) };
        }
        return {
          ...current,
          items: current.items.map((item) => "duration_ms" in item && item.id === updated.id ? updated : item),
        };
      });
      setPickerTracks((current) => current?.map((item) => item.id === updated.id ? updated : item) ?? null);
      onNotice(t(updated.liked ? "library.likedTrack" : "library.unlikedTrack", { title: updated.title }));
    } catch (caught) {
      setPage((current) => current && requestSerial.current === serial ? {
        ...current,
        items: current.items.map((item) => "duration_ms" in item && item.id === track.id ? track : item),
      } : current);
      onNotice(errorMessage(caught, t("common.requestFailed")), true);
    } finally {
      setLikeBusy((current) => {
        const next = new Set(current);
        next.delete(track.id);
        return next;
      });
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
      setScope({ kind: "folder", rootID: folder.root_id, rootTitle: folder.name, path: folder.path, title: folder.name });
    }
  }

  function leaveScope() {
    if (scope?.kind === "folder" && scope.path) {
      const path = parentFolderPath(scope.path);
      const title = path ? path.slice(path.lastIndexOf("/") + 1) : scope.rootTitle;
      setScope({ ...scope, path, title });
    } else {
      setScope(null);
    }
    setChildFolders([]);
  }

  const total = page?.total ?? 0;
  const limit = page?.limit || (scope || kind === "tracks" || kind === "most_played" ? trackPageSize : pageSize);
  const start = total ? offset + 1 : 0;
  const end = Math.min(offset + (page?.items.length ?? 0), total);

  useEffect(() => {
    if (page && offset > 0 && offset >= page.total) {
      setOffset(Math.floor(Math.max(0, page.total - 1) / limit) * limit);
    }
  }, [limit, offset, page]);

  return (
    <section className="library-shell" aria-labelledby="library-heading">
      <header className="library-heading-row">
        <div>
          <p className="library-eyebrow">{t("library.eyebrow")}</p>
          <h1 id="library-heading" className="page-heading">{t("library.heading")}</h1>
        </div>
        <label className="library-search">
          <span className="library-search-icon"><Icon name="search" /></span>
          <span className="library-visually-hidden">{t("library.searchLabel")}</span>
          <input className="input" type="search" value={searchInput} onChange={(event) => setSearchInput(event.target.value)} placeholder={t("library.searchPlaceholder")} autoComplete="off" />
        </label>
      </header>

      <nav className="library-tabs" aria-label={t("library.categoriesLabel")}>
        {tabs.map((tab) => (
          <button data-library-kind={tab.kind} key={tab.kind} className={`library-tab${kind === tab.kind && !scope && !likedOnly ? " library-tab-active" : ""}`} type="button" onClick={() => { setKind(tab.kind); setScope(null); setLikedOnly(false); }} aria-current={kind === tab.kind && !scope && !likedOnly ? "page" : undefined}>{t(tab.labelKey)}</button>
        ))}
        <button className={`library-tab library-liked-filter${likedOnly ? " library-tab-active" : ""}`} type="button" aria-pressed={likedOnly} onClick={() => { setLikedOnly((current) => !current); setKind("tracks"); setScope(null); }}><Icon name="heart" /> {t("library.likedFilter")}</button>
      </nav>

      {!scope && kind === "most_played" && !likedOnly && (
        <p className="library-most-played-help">{t("library.mostPlayedHelp")}</p>
      )}

      {scope && (
        <div className="library-detail-header">
          <div className="library-detail-toolbar" role="toolbar" aria-label={t("library.currentListActions")}>
            <button className="button button-ghost" type="button" onClick={leaveScope}><Icon name="back" /> {t(scope.kind === "folder" && scope.path ? "library.parentFolder" : "library.backToList")}</button>
            <button className="button button-primary" type="button" disabled={actionBusy || (!loading && !page?.total)} onClick={() => runScopeQueue("play")}><Icon name="play" /> {t("library.playAll")}</button>
            <button className="button button-ghost" type="button" disabled={actionBusy || (!loading && !page?.total)} onClick={() => runScopeQueue("next")}><Icon name="next" /> {t("library.playNext")}</button>
            <button className="button button-ghost" type="button" disabled={actionBusy || (!loading && !page?.total)} onClick={() => runScopeQueue("append")}><Icon name="append" /> {t("library.addToEnd")}</button>
            <button className="button button-ghost" type="button" disabled={actionBusy || (!loading && !page?.total)} onClick={openScopePicker}><Icon name="playlist" /> {t("library.addToSaved")}</button>
            {scope.kind === "album" && <NativeDownloadAction downloads={downloads} target={{ kind: "album", id: scope.id }} title={scope.title} />}
            {scope.kind === "folder" && <NativeDownloadAction downloads={downloads} target={{ kind: "folder", root_id: scope.rootID, path: scope.path }} title={scope.title} />}
          </div>
          <div className="library-detail-summary">
            {scope.kind === "album" ? (
              <Artwork
                id={scope.artworkID}
                label={t("library.artwork", { name: scope.title })}
                missingLabel={t("library.noArtwork", { name: scope.title })}
              />
            ) : <span className="library-detail-symbol" aria-hidden="true"><Icon name={scope.kind === "folder" ? "folder" : "music"} /></span>}
            <div className="library-detail-copy">
              <span className="muted">{t(scopeTypeKeys[scope.kind])}</span>
              <h2>{scope.title}</h2>
              {scope.kind === "album" && <p>{scope.subtitle}</p>}
              {scope.kind === "folder" && <p className="library-path">{scope.path || t("library.rootFolder")}</p>}
            </div>
          </div>
        </div>
      )}

      {error && <div className="library-error" role="alert"><p className="error-text">{error}</p><button className="button button-ghost" type="button" onClick={() => setReload((current) => current + 1)}>{t("common.retry")}</button></div>}
      {loading && <div className="library-loading" role="status">{t("library.loading")}</div>}

      {!loading && !error && page && page.items.length === 0 && childFolders.length === 0 && (
        <div className="empty-state">{t(search ? "library.noSearchResults" : likedOnly ? "library.noLikedTracks" : kind === "most_played" ? "library.noMostPlayedTracks" : "library.empty")}</div>
      )}

      {!loading && !error && !scope && kind === "albums" && (
        <div className="library-album-grid">
          {(page?.items as Album[] | undefined)?.map((album) => {
            const card = (
              <button className="library-album-card" type="button" key={album.id} onClick={() => openItem(album)}>
                <Artwork
                  id={album.artwork_id}
                  label={t("library.artwork", { name: album.title })}
                  missingLabel={t("library.noArtwork", { name: album.title })}
                />
                <span className="library-card-title">{album.title}</span>
                <span className="library-card-meta">
                  {album.artist || t("library.unknownArtist")} · {t(album.track_count === 1 ? "library.oneTrack" : "library.manyTracks", { count: numberFormatter.format(album.track_count) })}
                </span>
              </button>
            );
            return downloads.available ? (
              <article className="library-album-tile" key={album.id}>
                {card}
                <NativeDownloadAction downloads={downloads} target={{ kind: "album", id: album.id }} title={album.title} />
              </article>
            ) : card;
          })}
        </div>
      )}

      {!loading && !error && !scope && kind === "artists" && (
        <div className="library-name-grid">
          {(page?.items as Artist[] | undefined)?.map((artist) => <button className="library-name-card" type="button" key={artist.id} onClick={() => openItem(artist)}><span className="library-name-mark"><Icon name="music" /></span><span><strong>{artist.name}</strong><small>{t(artist.track_count === 1 ? "library.oneTrack" : "library.manyTracks", { count: numberFormatter.format(artist.track_count) })}</small></span></button>)}
        </div>
      )}

      {!loading && !error && !scope && kind === "genres" && (
        <div className="library-name-grid">
          {(page?.items as Genre[] | undefined)?.map((genre) => <button className="library-name-card" type="button" key={genre.id} onClick={() => openItem(genre)}><span className="library-name-mark"><Icon name="music" /></span><span><strong>{genre.name}</strong><small>{t(genre.track_count === 1 ? "library.oneTrack" : "library.manyTracks", { count: numberFormatter.format(genre.track_count) })}</small></span></button>)}
        </div>
      )}

      {!loading && !error && !scope && kind === "folders" && (
        <div className="library-folder-list">
          {(page?.items as Folder[] | undefined)?.map((folder) => (
            <div className="library-folder-item" key={`${folder.root_id}:${folder.path}`}>
              <button className="library-folder-row" type="button" onClick={() => openItem(folder)}><span className="library-folder-icon"><Icon name="folder" /></span><span><strong>{folder.name}</strong><small>{folder.path || t("library.rootFolder")}</small></span><span className="library-folder-count">{t(folder.track_count === 1 ? "library.oneTrack" : "library.manyTracks", { count: numberFormatter.format(folder.track_count) })}</span></button>
              <NativeDownloadAction downloads={downloads} target={{ kind: "folder", root_id: folder.root_id, path: folder.path }} title={folder.name} disabled={folder.track_count === 0} />
            </div>
          ))}
        </div>
      )}

      {!loading && !error && scope?.kind === "folder" && childFolders.length > 0 && (
        <div className="library-child-folders" aria-label={t("library.childFolders")}>
          {childFolders.map((folder) => (
            <div className="library-folder-item" key={`${folder.root_id}:${folder.path}`}>
              <button className="library-folder-row" type="button" onClick={() => setScope({ kind: "folder", rootID: folder.root_id, rootTitle: scope.rootTitle, path: folder.path, title: folder.name })}><span className="library-folder-icon"><Icon name="folder" /></span><span><strong>{folder.name}</strong><small>{folder.path}</small></span><span className="library-folder-count">{t(folder.track_count === 1 ? "library.oneTrack" : "library.manyTracks", { count: numberFormatter.format(folder.track_count) })}</span></button>
              <NativeDownloadAction downloads={downloads} target={{ kind: "folder", root_id: folder.root_id, path: folder.path }} title={folder.name} disabled={folder.track_count === 0} />
            </div>
          ))}
        </div>
      )}

      {!loading && !error && (scope || kind === "tracks" || kind === "most_played") && visibleTracks.length > 0 && (
        <div className="library-track-list" role="list" aria-label={t("library.trackList")}>
          {visibleTracks.map((track, index) => (
            <article className={`library-track-row${track.available ? "" : " library-track-unavailable"}`} role="listitem" key={track.id}>
              <span className="library-track-number">{scope?.kind === "album" ? `${track.disc > 1 ? `${track.disc}-` : ""}${track.track || offset + index + 1}` : numberFormatter.format(offset + index + 1)}</span>
              <Artwork
                id={track.artwork_id}
                label={t("library.artwork", { name: track.album || track.title })}
                missingLabel={t("library.noArtwork", { name: track.album || track.title })}
                compact
              />
              <div className="library-track-main">
                <strong>{track.title}</strong>
                <span>{track.artist || t("library.unknownArtist")}</span>
                {kind === "most_played" && !scope && (
                  <span className="library-track-play-count">{t(track.play_count === 1 ? "library.onePlay" : "library.manyPlays", { count: numberFormatter.format(track.play_count) })}</span>
                )}
              </div>
              <div className="library-track-album"><span>{track.album || t("library.unknownAlbum")}</span>{!track.available && <small>{t("library.fileUnavailable")}</small>}</div>
              <time className="library-track-duration">{formatDuration(track.duration_ms)}</time>
              <div className="library-track-actions" aria-label={t("library.trackActions", { title: track.title })}>
                <button className="library-like-button" type="button" title={t(track.liked ? "library.unlikeTitle" : "library.likeTitle")} aria-label={t(track.liked ? "library.unlikeTrack" : "library.likeTrack", { title: track.title })} aria-pressed={track.liked} disabled={likeBusy.has(track.id)} onClick={() => toggleLiked(track)}><Icon name="heart" /></button>
                <NativeDownloadAction downloads={downloads} target={{ kind: "track", id: track.id }} title={track.title} disabled={!track.available} variant="icon" />
                <button type="button" title={t("library.playNow")} aria-label={t("library.playNowTrack", { title: track.title })} disabled={!track.available || actionBusy} onClick={() => runQueue([track], "play")}><Icon name="play" /></button>
                <button type="button" title={t("library.playNextTitle")} aria-label={t("library.playNextTrack", { title: track.title })} disabled={!track.available || actionBusy} onClick={() => runQueue([track], "next")}><Icon name="next" /></button>
                <button type="button" title={t("library.addToEnd")} aria-label={t("library.addEndTrack", { title: track.title })} disabled={!track.available || actionBusy} onClick={() => runQueue([track], "append")}><Icon name="append" /></button>
                <button type="button" title={t("library.addToSaved")} aria-label={t("library.addSavedTrack", { title: track.title })} disabled={!track.available || actionBusy} onClick={() => openTrackPicker(track)}><Icon name="playlist" /></button>
                <button type="button" title={t("library.trackInfo")} aria-label={t("library.viewTrackInfo", { title: track.title })} onClick={(event) => { event.stopPropagation(); setInfoTrackID(track.id); }}><Icon name="info" /></button>
              </div>
            </article>
          ))}
        </div>
      )}

      {!loading && !error && total > limit && (
        <nav className="library-pagination" aria-label={t("library.pagination")}>
          <button className="button button-ghost" type="button" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>{t("common.previous")}</button>
          <span>{numberFormatter.format(start)}–{numberFormatter.format(end)} / {numberFormatter.format(total)}</span>
          <button className="button button-ghost" type="button" disabled={offset + limit >= total} onClick={() => setOffset(offset + limit)}>{t("common.next")}</button>
        </nav>
      )}

      {pickerTracks && (
        <div className="library-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.currentTarget === event.target) setPickerTracks(null); }}>
          <section className="library-dialog" role="dialog" aria-modal="true" aria-labelledby="library-picker-title">
            <div className="library-dialog-heading"><div><p className="library-eyebrow">{t("library.savedPlaylists")}</p><h2 id="library-picker-title">{t(pickerTracks.length === 1 ? "library.addOneTrack" : "library.addManyTracks", { count: numberFormatter.format(pickerTracks.length) })}</h2></div><button className="button button-ghost" type="button" onClick={() => setPickerTracks(null)}>{t("common.close")}</button></div>
            {playlists.length > 0 && <><label className="library-field"><span>{t("library.playlistToAdd")}</span><select className="input" value={pickerTarget} onChange={(event) => setPickerTarget(event.target.value)} autoFocus>{playlists.map((playlist) => <option key={playlist.id} value={playlist.id}>{playlist.name}</option>)}</select></label><button className="button button-primary library-dialog-submit" type="button" disabled={actionBusy || !pickerTarget} onClick={addToPlaylist}>{t("library.addToSelected")}</button></>}
            <div className="library-dialog-divider"><span>{t("library.orCreate")}</span></div>
            <form className="library-create-inline" onSubmit={(event) => { event.preventDefault(); void createPlaylistWithTracks(); }}><label className="library-field"><span>{t("library.newPlaylistName")}</span><input className="input" value={newPlaylistName} onChange={(event) => setNewPlaylistName(event.target.value)} maxLength={120} autoFocus={playlists.length === 0} /></label><button className="button button-ghost" type="submit" disabled={actionBusy || !newPlaylistName.trim()}>{t("library.createAndAdd")}</button></form>
            {pickerError && <p className="error-text" role="alert">{pickerError}</p>}
          </section>
        </div>
      )}
      <TrackInfoDialog trackId={infoTrackID} revision={revision} onNotice={onNotice} onClose={() => setInfoTrackID(null)} downloads={downloads} />
    </section>
  );
}
