export interface Track {
  id: string;
  title: string;
  artist: string;
  album: string;
  album_artist: string;
  album_id: string;
  disc: number;
  track: number;
  genres: string[];
  duration_ms: number;
  format: string;
  mime: string;
  artwork_id: string;
  root_id: string;
  path: string;
  available: boolean;
  size: number;
  modified_at: string;
}

export interface TrackInfo {
  track: Track;
  audio: {
    codec: string;
    sample_rate: number | null;
    channels: number | null;
    bits_per_sample: number | null;
    bit_rate: number | null;
  };
  tags: Record<string, string[]>;
}

export interface Album {
  id: string;
  title: string;
  artist: string;
  artwork_id: string;
  track_count: number;
}

export interface Artist {
  id: string;
  name: string;
  track_count: number;
}

export interface Genre {
  id: string;
  name: string;
  track_count: number;
}

export interface Folder {
  root_id: string;
  path: string;
  name: string;
  track_count: number;
}

export interface Page<T> {
  items: T[];
  total: number;
  offset: number;
  limit: number;
}

export interface Playlist {
  id: string;
  name: string;
  revision: number;
  track_ids: string[];
  tracks?: Track[];
  updated_at: string;
}

export type ScanStatus = "queued" | "running" | "complete" | "failed" | "cancelled";

export interface ScanJob {
  id: string;
  status: ScanStatus;
  discovered: number;
  processed: number;
  added: number;
  updated: number;
  unavailable: number;
  errors: number;
  started_at: string;
  finished_at: string;
  error: string;
}

export interface Capabilities {
  play: boolean;
  pause: boolean;
  stop: boolean;
  seek: boolean;
}

export type OutputProtocol = "upnp" | "airplay";

export interface Device {
  id: string;
  name: string;
  protocol: OutputProtocol;
  manufacturer: string;
  model: string;
  address: string;
  online: boolean;
  last_seen: string;
  capabilities: Capabilities;
  protocol_info: string[];
  pairing_required: boolean;
  password_required: boolean;
}

export type PlaybackState =
  | "stopped"
  | "starting"
  | "playing"
  | "paused"
  | "unavailable"
  | "error";

export interface StatusWarning {
  id: number;
  message: string;
}

export interface PlayerState {
  revision: number;
  state: PlaybackState;
  renderer_id: string;
  current_entry_id: string;
  track: Track | null;
  position_ms: number;
  duration_ms: number;
  observed_at: string;
  pending_command: string;
  error: string;
  status_warning?: StatusWarning;
  capabilities: Capabilities;
}

export type QueueEntryStatus = "pending" | "playing" | "completed" | "error";

export interface QueueEntry {
  id: string;
  track_id: string;
  track: Track;
  status: QueueEntryStatus;
}

export interface QueueState {
  revision: number;
  entries: QueueEntry[];
}

export interface PairingRequest {
  pin?: string;
  password?: string;
}

export interface PairingStatus {
  required: boolean;
  prompt: string;
}

export interface ConfigRoot {
  id: string;
  name: string;
  path: string;
}

export interface ServerConfig {
  version: number;
  server_name?: string;
  data_dir: string;
  http: {
    enabled: boolean;
    address: string;
  };
  https: {
    enabled: boolean;
    address: string;
    certificate_file: string;
    private_key_file: string;
  };
  library_roots: ConfigRoot[];
  network: {
    interfaces: string[];
    discovery_interval_seconds: number;
    poll_interval_seconds: number;
    allowed_cidrs: string[];
  };
  media: {
    base_url: string;
    ffmpeg_path: string;
    transcode: boolean;
  };
  airplay: {
    enabled: boolean;
    helper_path: string;
  };
}

export interface SessionUser {
  id: string;
  username: string;
}

export interface Session {
  authenticated: boolean;
  user?: SessionUser;
}

export interface ConfigDocument {
  config: ServerConfig;
  revision: string;
  restart_required?: boolean;
}
