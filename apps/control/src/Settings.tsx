import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { api, ApiError } from "./api";
import type { ConfigDocument, ConfigRoot, ScanJob, ServerConfig } from "./types";

interface SettingsProps {
  configRevision: number;
  libraryRevision: number;
  onNotice: (message: string, error?: boolean) => void;
  onSignedOut: () => void;
}

function requestMessage(error: unknown): string {
  return error instanceof ApiError ? error.message : "요청을 완료하지 못했습니다.";
}

function listText(items: string[]): string {
  return items.join("\n");
}

function parseList(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function newRootID(): string {
  const random = new Uint8Array(16);
  crypto.getRandomValues(random);
  return `root-${Array.from(random, (value) => value.toString(16).padStart(2, "0")).join("")}`;
}

const scanStatus: Record<ScanJob["status"], string> = {
  queued: "대기 중",
  running: "스캔 중",
  complete: "완료",
  failed: "실패",
  cancelled: "취소됨",
};

export default function Settings({ configRevision, libraryRevision, onNotice, onSignedOut }: SettingsProps) {
  const [document, setDocument] = useState<ConfigDocument | null>(null);
  const [draft, setDraft] = useState<ServerConfig | null>(null);
  const [scans, setScans] = useState<ScanJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [scanBusy, setScanBusy] = useState("");
  const [error, setError] = useState("");
  const [remoteConfigPending, setRemoteConfigPending] = useState(false);
  const [restartRequired, setRestartRequired] = useState(false);
  const [interfacesText, setInterfacesText] = useState("");
  const [cidrsText, setCidrsText] = useState("");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [passwordBusy, setPasswordBusy] = useState(false);
  const configDirtyRef = useRef(false);
  configDirtyRef.current = Boolean(
    document
    && draft
    && (
      JSON.stringify(draft) !== JSON.stringify(document.config)
      || interfacesText !== listText(document.config.network.interfaces)
      || cidrsText !== listText(document.config.network.allowed_cidrs)
    ),
  );

  const loadConfig = useCallback(async () => {
    try {
      const next = await api<ConfigDocument>("/config");
      setDocument(next);
      setDraft(structuredClone(next.config));
      setInterfacesText(listText(next.config.network.interfaces));
      setCidrsText(listText(next.config.network.allowed_cidrs));
      setError("");
      setRemoteConfigPending(false);
    } catch (requestError) {
      setError(requestMessage(requestError));
    } finally {
      setLoading(false);
    }
  }, []);

  const loadScans = useCallback(async () => {
    try {
      const result = await api<{ items: ScanJob[] }>("/library/scans");
      setScans(result.items ?? []);
    } catch (requestError) {
      setError(requestMessage(requestError));
    }
  }, []);

  useEffect(() => {
    if (configDirtyRef.current) {
      setRemoteConfigPending(true);
      return;
    }
    void loadConfig();
  }, [configRevision, loadConfig]);

  useEffect(() => {
    void loadScans();
  }, [libraryRevision, loadScans]);

  useEffect(() => {
    if (!scans.some((scan) => scan.status === "queued" || scan.status === "running")) return;
    const timer = window.setInterval(() => void loadScans(), 2500);
    return () => window.clearInterval(timer);
  }, [loadScans, scans]);

  function updateDraft(update: (config: ServerConfig) => ServerConfig) {
    setDraft((current) => (current ? update(current) : current));
  }

  async function saveConfig(event: FormEvent) {
    event.preventDefault();
    if (!document || !draft) return;
    setSaving(true);
    try {
      const next = await api<ConfigDocument>("/config", {
        method: "PUT",
        body: JSON.stringify({ config: draft, revision: document.revision }),
      });
      setDocument(next);
      setDraft(structuredClone(next.config));
      setInterfacesText(listText(next.config.network.interfaces));
      setCidrsText(listText(next.config.network.allowed_cidrs));
      setRestartRequired(Boolean(next.restart_required));
      setError("");
      setRemoteConfigPending(false);
      onNotice(
        next.restart_required
          ? "설정을 저장했습니다. 서버 이름과 접속 설정 등의 적용에는 서버 재시작이 필요합니다."
          : "설정을 저장했습니다.",
      );
    } catch (requestError) {
      const conflict = requestError instanceof ApiError && requestError.status === 409;
      const message = conflict
        ? "설정이 다른 곳에서 변경되었습니다. 최신 설정을 다시 불러왔습니다."
        : requestMessage(requestError);
      setError(message);
      onNotice(message, true);
      if (conflict) await loadConfig();
    } finally {
      setSaving(false);
    }
  }

  function addRoot() {
    const root: ConfigRoot = {
      id: newRootID(),
      name: "",
      path: "",
    };
    updateDraft((config) => ({
      ...config,
      library_roots: [...config.library_roots, root],
    }));
  }

  function changeRoot(index: number, field: "name" | "path", value: string) {
    updateDraft((config) => ({
      ...config,
      library_roots: config.library_roots.map((root, rootIndex) =>
        rootIndex === index ? { ...root, [field]: value } : root,
      ),
    }));
  }

  function removeRoot(index: number) {
    updateDraft((config) => ({
      ...config,
      library_roots: config.library_roots.filter((_, rootIndex) => rootIndex !== index),
    }));
  }

  async function startScan() {
    setScanBusy("new");
    try {
      const job = await api<ScanJob>("/library/scans", {
        method: "POST",
        body: JSON.stringify({}),
      });
      setScans((current) => [job, ...current.filter((item) => item.id !== job.id)]);
      onNotice("음악 보관함 스캔을 시작했습니다.");
    } catch (requestError) {
      onNotice(requestMessage(requestError), true);
    } finally {
      setScanBusy("");
    }
  }

  async function cancelScan(id: string) {
    setScanBusy(id);
    try {
      await api<void>(`/library/scans/${encodeURIComponent(id)}`, { method: "DELETE" });
      await loadScans();
      onNotice("스캔 취소를 요청했습니다.");
    } catch (requestError) {
      onNotice(requestMessage(requestError), true);
    } finally {
      setScanBusy("");
    }
  }

  async function changePassword(event: FormEvent) {
    event.preventDefault();
    if (newPassword !== confirmPassword) {
      onNotice("새 비밀번호가 서로 일치하지 않습니다.", true);
      return;
    }
    setPasswordBusy(true);
    try {
      await api<void>("/account/password", {
        method: "POST",
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      });
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      onSignedOut();
    } catch (requestError) {
      onNotice(requestMessage(requestError), true);
    } finally {
      setPasswordBusy(false);
    }
  }

  if (loading && !draft) {
    return <div className="loading-block" aria-live="polite">설정을 불러오는 중…</div>;
  }

  if (!draft) {
    return (
      <section className="content-section">
        <div className="inline-error" role="alert">
          <span>{error || "설정을 불러오지 못했습니다."}</span>
          <button className="button button-ghost" type="button" onClick={() => void loadConfig()}>
            다시 시도
          </button>
        </div>
      </section>
    );
  }

  return (
    <section className="content-section settings-page" aria-labelledby="settings-heading">
      <header className="page-heading">
        <p className="eyebrow">서버 관리</p>
        <h1 id="settings-heading">설정</h1>
        <p className="muted">음악 위치와 접속 방식을 이 서버에 안전하게 저장합니다.</p>
      </header>

      {error && <p className="error-text" role="alert">{error}</p>}
      {remoteConfigPending && (
        <div className="inline-error" role="alert">
          <span>다른 곳에서 설정을 변경했습니다. 편집 중인 내용은 아직 유지하고 있습니다.</span>
          <button className="button button-ghost" type="button" onClick={() => void loadConfig()}>
            최신 설정 불러오기
          </button>
        </div>
      )}
      {restartRequired && (
        <div className="restart-banner" role="status">
          저장된 서버 이름과 접속 설정 등을 적용하려면 jastreamer 서버를 재시작하세요.
        </div>
      )}

      <form className="settings-form" onSubmit={(event) => void saveConfig(event)}>
        <section className="settings-card">
          <h2>서버 이름</h2>
          <label className="field-label" htmlFor="server-name">데스크톱 앱에 표시할 이름</label>
          <input
            className="input"
            id="server-name"
            value={draft.server_name ?? ""}
            maxLength={64}
            placeholder="비워 두면 서버의 호스트 이름을 사용합니다"
            onChange={(event) => updateDraft((config) => ({ ...config, server_name: event.target.value }))}
          />
          <p className="field-help">같은 네트워크에서 서버를 찾을 때 표시합니다. 변경 후 서버 재시작이 필요합니다.</p>
        </section>

        <section className="settings-card">
          <h2>서버 저장소</h2>
          <label className="field-label" htmlFor="data-directory">데이터 디렉터리</label>
          <input
            className="input"
            id="data-directory"
            value={draft.data_dir}
            required
            onChange={(event) => updateDraft((config) => ({ ...config, data_dir: event.target.value }))}
          />
          <p className="field-help">데이터베이스와 아트워크 캐시 위치입니다. 변경 후 재시작이 필요할 수 있습니다.</p>
        </section>

        <section className="settings-card protocol-settings">
          <div className="settings-card-heading">
            <div>
              <h2>HTTP</h2>
              <p className="muted">신뢰하는 사설 네트워크에서 접속합니다.</p>
            </div>
            <label className="switch-label">
              <input
                type="checkbox"
                checked={draft.http.enabled}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  http: { ...config.http, enabled: event.target.checked },
                }))}
              />
              사용
            </label>
          </div>
          <label className="field-label" htmlFor="http-address">수신 주소</label>
          <input
            className="input"
            id="http-address"
            value={draft.http.address}
            disabled={!draft.http.enabled}
            required={draft.http.enabled}
            placeholder=":8080"
            onChange={(event) => updateDraft((config) => ({
              ...config,
              http: { ...config.http, address: event.target.value },
            }))}
          />
        </section>

        <section className="settings-card protocol-settings">
          <div className="settings-card-heading">
            <div>
              <h2>HTTPS</h2>
              <p className="muted">서버에서 읽을 수 있는 PEM 인증서가 필요합니다.</p>
            </div>
            <label className="switch-label">
              <input
                type="checkbox"
                checked={draft.https.enabled}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  https: { ...config.https, enabled: event.target.checked },
                }))}
              />
              사용
            </label>
          </div>
          <div className="field-grid">
            <label>
              <span className="field-label">수신 주소</span>
              <input
                className="input"
                value={draft.https.address}
                disabled={!draft.https.enabled}
                required={draft.https.enabled}
                placeholder=":8443"
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  https: { ...config.https, address: event.target.value },
                }))}
              />
            </label>
            <label>
              <span className="field-label">인증서 파일</span>
              <input
                className="input"
                value={draft.https.certificate_file}
                disabled={!draft.https.enabled}
                required={draft.https.enabled}
                placeholder="/etc/jastreamer/server.crt"
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  https: { ...config.https, certificate_file: event.target.value },
                }))}
              />
            </label>
            <label>
              <span className="field-label">개인 키 파일</span>
              <input
                className="input"
                value={draft.https.private_key_file}
                disabled={!draft.https.enabled}
                required={draft.https.enabled}
                placeholder="/etc/jastreamer/server.key"
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  https: { ...config.https, private_key_file: event.target.value },
                }))}
              />
            </label>
          </div>
        </section>

        <section className="settings-card roots-settings">
          <div className="settings-card-heading">
            <div>
              <h2>음악 폴더</h2>
              <p className="muted">서버에 마운트된 절대 경로만 지정하세요.</p>
            </div>
            <button className="button button-ghost" type="button" onClick={addRoot}>폴더 추가</button>
          </div>
          {draft.library_roots.length === 0 ? (
            <p className="empty-inline">등록된 음악 폴더가 없습니다.</p>
          ) : (
            <div className="root-list">
              {draft.library_roots.map((root, index) => (
                <div className="root-row" key={root.id}>
                  <label>
                    <span className="field-label">표시 이름</span>
                    <input
                      className="input"
                      value={root.name}
                      required
                      placeholder="거실 음악"
                      onChange={(event) => changeRoot(index, "name", event.target.value)}
                    />
                  </label>
                  <label>
                    <span className="field-label">서버 경로</span>
                    <input
                      className="input"
                      value={root.path}
                      required
                      placeholder="/music"
                      onChange={(event) => changeRoot(index, "path", event.target.value)}
                    />
                  </label>
                  <button
                    className="button button-ghost danger-button"
                    type="button"
                    aria-label={`${root.name || "음악 폴더"} 삭제`}
                    onClick={() => removeRoot(index)}
                  >
                    삭제
                  </button>
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="settings-card">
          <h2>네트워크 검색</h2>
          <div className="field-grid">
            <label>
              <span className="field-label">네트워크 인터페이스</span>
              <textarea
                className="input"
                rows={3}
                value={interfacesText}
                placeholder="비워 두면 자동 선택"
                onChange={(event) => {
                  setInterfacesText(event.target.value);
                  updateDraft((config) => ({
                    ...config,
                    network: { ...config.network, interfaces: parseList(event.target.value) },
                  }));
                }}
              />
            </label>
            <label>
              <span className="field-label">허용 CIDR</span>
              <textarea
                className="input"
                rows={3}
                value={cidrsText}
                placeholder="비워 두면 사설/루프백 네트워크만 허용"
                onChange={(event) => {
                  setCidrsText(event.target.value);
                  updateDraft((config) => ({
                    ...config,
                    network: { ...config.network, allowed_cidrs: parseList(event.target.value) },
                  }));
                }}
              />
            </label>
            <label>
              <span className="field-label">기기 검색 간격(초)</span>
              <input
                className="input"
                type="number"
                min={5}
                value={draft.network.discovery_interval_seconds}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  network: { ...config.network, discovery_interval_seconds: Number(event.target.value) },
                }))}
              />
            </label>
            <label>
              <span className="field-label">재생 상태 확인 간격(초)</span>
              <input
                className="input"
                type="number"
                min={1}
                value={draft.network.poll_interval_seconds}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  network: { ...config.network, poll_interval_seconds: Number(event.target.value) },
                }))}
              />
            </label>
          </div>
        </section>

        <section className="settings-card protocol-settings">
          <div className="settings-card-heading">
            <div>
              <h2>AirPlay 출력</h2>
              <p className="muted">AirPlay 기기로 음악과 아트워크를 전송합니다.</p>
            </div>
            <label className="switch-label">
              <input
                type="checkbox"
                checked={draft.airplay.enabled}
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  airplay: { ...config.airplay, enabled: event.target.checked },
                }))}
              />
              사용
            </label>
          </div>
          <label className="field-label" htmlFor="airplay-helper-path">AirPlay helper 경로</label>
          <input
            className="input"
            id="airplay-helper-path"
            value={draft.airplay.helper_path}
            disabled={!draft.airplay.enabled}
            required={draft.airplay.enabled}
            placeholder="/path/to/airplay-helper"
            onChange={(event) => updateDraft((config) => ({
              ...config,
              airplay: { ...config.airplay, helper_path: event.target.value },
            }))}
          />
          <p className="field-help">서버에서 실행할 수 있는 AirPlay helper 파일의 절대 경로입니다. 변경 후 재시작이 필요합니다.</p>
        </section>

        <section className="settings-card">
          <h2>미디어 전송</h2>
          <div className="field-grid">
            <label>
              <span className="field-label">기기에서 접근할 기본 URL</span>
              <input
                className="input"
                value={draft.media.base_url}
                placeholder="비워 두면 네트워크에 맞게 자동 선택"
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  media: { ...config.media, base_url: event.target.value },
                }))}
              />
            </label>
            <label>
              <span className="field-label">FFmpeg 경로</span>
              <input
                className="input"
                value={draft.media.ffmpeg_path}
                placeholder="/usr/bin/ffmpeg"
                onChange={(event) => updateDraft((config) => ({
                  ...config,
                  media: { ...config.media, ffmpeg_path: event.target.value },
                }))}
              />
            </label>
          </div>
          <label className="switch-label inline-switch">
            <input
              type="checkbox"
              checked={draft.media.transcode}
              onChange={(event) => updateDraft((config) => ({
                ...config,
                media: { ...config.media, transcode: event.target.checked },
              }))}
            />
            기기가 원본 형식을 지원하지 않을 때 변환 허용
          </label>
        </section>

        <div className="settings-save-row">
          <button
            className="button button-ghost"
            type="button"
            disabled={saving}
            onClick={() => {
              setDraft(structuredClone(document?.config ?? draft));
              setInterfacesText(listText((document?.config ?? draft).network.interfaces));
              setCidrsText(listText((document?.config ?? draft).network.allowed_cidrs));
              setError("");
            }}
          >
            변경 취소
          </button>
          <button className="button button-primary" type="submit" disabled={saving}>
            {saving ? "저장 중…" : "설정 저장"}
          </button>
        </div>
      </form>

      <section className="settings-card scan-settings" aria-labelledby="scan-heading">
        <div className="settings-card-heading">
          <div>
            <h2 id="scan-heading">보관함 스캔</h2>
            <p className="muted">추가·변경·누락된 음악을 확인합니다.</p>
          </div>
          <button
            className="button button-primary"
            type="button"
            disabled={Boolean(scanBusy) || scans.some((scan) => scan.status === "queued" || scan.status === "running")}
            onClick={() => void startScan()}
          >
            {scanBusy === "new" ? "시작 중…" : "지금 스캔"}
          </button>
        </div>
        {scans.length === 0 ? (
          <p className="empty-inline">아직 스캔 기록이 없습니다.</p>
        ) : (
          <ul className="scan-list">
            {scans.map((scan) => {
              const active = scan.status === "queued" || scan.status === "running";
              return (
                <li key={scan.id}>
                  <div className="scan-summary">
                    <strong>{scanStatus[scan.status]}</strong>
                    <span>처리 {scan.processed.toLocaleString()} / 발견 {scan.discovered.toLocaleString()}</span>
                    <span>추가 {scan.added.toLocaleString()} · 갱신 {scan.updated.toLocaleString()} · 누락 {scan.unavailable.toLocaleString()}</span>
                    {scan.error && <span className="error-text">{scan.error}</span>}
                  </div>
                  {active && (
                    <button
                      className="button button-ghost danger-button"
                      type="button"
                      disabled={scanBusy === scan.id}
                      onClick={() => void cancelScan(scan.id)}
                    >
                      취소
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <section className="settings-card account-settings" aria-labelledby="password-heading">
        <h2 id="password-heading">비밀번호 변경</h2>
        <p className="muted">변경이 끝나면 모든 기기에서 로그아웃됩니다.</p>
        <form className="password-form" onSubmit={(event) => void changePassword(event)}>
          <label>
            <span className="field-label">현재 비밀번호</span>
            <input
              className="input"
              type="password"
              autoComplete="current-password"
              value={currentPassword}
              required
              onChange={(event) => setCurrentPassword(event.target.value)}
            />
          </label>
          <label>
            <span className="field-label">새 비밀번호</span>
            <input
              className="input"
              type="password"
              autoComplete="new-password"
              minLength={10}
              value={newPassword}
              required
              onChange={(event) => setNewPassword(event.target.value)}
            />
          </label>
          <label>
            <span className="field-label">새 비밀번호 확인</span>
            <input
              className="input"
              type="password"
              autoComplete="new-password"
              minLength={10}
              value={confirmPassword}
              required
              onChange={(event) => setConfirmPassword(event.target.value)}
            />
          </label>
          <button className="button button-primary" type="submit" disabled={passwordBusy}>
            {passwordBusy ? "변경 중…" : "비밀번호 변경"}
          </button>
        </form>
      </section>
    </section>
  );
}
