import { APIError, idempotencyKey, requestJSON, session } from "./api.js";

const routes = {
  config: "/api/v1/config",
  roots: "/api/v1/catalog/roots",
  scans: "/api/v1/catalog/scans",
  zones: "/api/v1/zones",
  devices: "/api/v1/devices",
};

const state = {
  config: null,
  etag: "",
  roots: [],
  jobs: [],
  zones: [],
  renderers: [],
  devices: [],
  pendingIntent: null,
};

const byId = (id) => document.getElementById(id);
const lines = (value) => value.split("\n").map((item) => item.trim()).filter(Boolean);
const text = (value) => value === undefined || value === null ? "" : String(value);
const sectionIDs = ["settings", "catalog", "renderers", "devices"];
let navigationFrame = 0;

function setCurrentSection(sectionID) {
  for (const id of sectionIDs) {
    const link = document.querySelector(`.section-nav a[href="#${id}"]`);
    if (id === sectionID) link.setAttribute("aria-current", "location");
    else link.removeAttribute("aria-current");
  }
}

function updateCurrentSection() {
  navigationFrame = 0;
  const main = byId("main-content");
  if (byId("admin-shell").hidden) return;
  const anchor = window.innerWidth <= 760 ? 120 : main.getBoundingClientRect().top + 80;
  const current = sectionIDs.reduce((nearest, id) => {
    const distance = Math.abs(byId(id).getBoundingClientRect().top - anchor);
    return distance < nearest.distance ? { id, distance } : nearest;
  }, { id: "settings", distance: Number.POSITIVE_INFINITY });
  setCurrentSection(current.id);
}

function scheduleCurrentSection() {
  if (!navigationFrame) navigationFrame = requestAnimationFrame(updateCurrentSection);
}

function showAdministration() {
  byId("login-panel").hidden = true;
  byId("admin-shell").hidden = false;
  const requested = sectionIDs.includes(location.hash.slice(1)) ? location.hash.slice(1) : "settings";
  setCurrentSection(requested);
  if (requested === "settings") byId("main-content").scrollTop = 0;
  else byId(requested).scrollIntoView({ block: "start" });
  byId("main-content").focus();
  scheduleCurrentSection();
}

function setMessage(target, value, kind = "") {
  target.textContent = value;
  target.classList.toggle("error", kind === "error");
  target.classList.toggle("success", kind === "success");
}

function setBusy(button, busy, pendingLabel) {
  if (!button.dataset.idleLabel) button.dataset.idleLabel = button.textContent;
  button.disabled = busy;
  button.setAttribute("aria-busy", String(busy));
  button.textContent = busy ? pendingLabel : button.dataset.idleLabel;
}

function endSession(reason = "") {
  session.clear();
  state.config = null;
  state.etag = "";
  state.pendingIntent = null;
  byId("admin-shell").hidden = true;
  byId("login-panel").hidden = false;
  byId("admin-token").value = "";
  const loginError = byId("login-error");
  setMessage(loginError, reason, reason ? "error" : "");
  if (reason) loginError.focus();
  else byId("admin-token").focus();
}

function reportError(error, target) {
  if (error instanceof APIError && error.status === 401) {
    endSession("Administrator session ended. Enter a valid administrator token to continue.");
    return;
  }
  const detail = error instanceof APIError
    ? `${error.code}: ${error.message}${error.field ? ` (${error.field})` : ""}`
    : `The request could not be completed: ${error instanceof Error ? error.message : "unknown error"}.`;
  setMessage(target, detail, "error");
}

function emptyItem(label) {
  const item = document.createElement("li");
  item.className = "empty";
  item.textContent = label;
  return item;
}

function inventoryCopy(nameValue, detailValue) {
  const copy = document.createElement("div");
  copy.className = "inventory-copy";
  const name = document.createElement("strong");
  name.textContent = nameValue;
  const detail = document.createElement("p");
  detail.textContent = detailValue;
  copy.append(name, detail);
  return copy;
}

function renderConfig(snapshot, etag) {
  state.config = snapshot;
  state.etag = etag;
  const settings = snapshot.settings;
  const locks = snapshot.locks;
  byId("server-name").textContent = settings.display_name;
  byId("display-name").value = settings.display_name;
  byId("pairing-ttl").value = text(settings.pairing_ttl_seconds);
  byId("control-origins").value = (settings.control_origins || []).join("\n");
  byId("upnp-interfaces").value = (settings.upnp_interfaces || []).join("\n");
  byId("ffmpeg-path").value = settings.ffmpeg_path;
  byId("k17-http-enabled").checked = settings.k17_http.enabled;
  byId("k17-listener-address").value = settings.k17_http.listener_address;
  byId("listen-address").value = locks.listen_address;
  byId("certificate-fingerprint").value = locks.certificate_fingerprint;
  byId("certificate-sans").value = (locks.certificate_sans || []).join("\n");
  byId("data-directory").value = locks.data_directory;

  const controls = {
    display_name: ["display-name"],
    pairing_ttl_seconds: ["pairing-ttl"],
    control_origins: ["control-origins"],
    upnp_interfaces: ["upnp-interfaces"],
    ffmpeg_path: ["ffmpeg-path"],
    k17_http: ["k17-http-enabled", "k17-listener-address"],
  };
  const locked = new Set(locks.environment_locked_fields);
  for (const [field, ids] of Object.entries(controls)) {
    for (const id of ids) {
      byId(id).disabled = locked.has(field);
      byId(id).setAttribute("aria-describedby", locked.has(field) ? "locked-fields" : "");
    }
  }
  byId("locked-fields").textContent = locked.size
    ? `Environment-locked settings: ${[...locked].join(", ")}.`
    : "No editable settings are locked by the environment.";
  byId("restart-banner").hidden = !snapshot.restart_required;
  byId("restart-fields").textContent = snapshot.restart_fields.join(", ");
  renderAudioStatus();
}

function captureSettingsIntent() {
  const current = state.config.settings;
  const locked = new Set(state.config.locks.environment_locked_fields || []);
  const intent = {};
  const displayName = byId("display-name").value.trim();
  const pairingTTL = Number(byId("pairing-ttl").value);
  const origins = lines(byId("control-origins").value);
  const interfaces = lines(byId("upnp-interfaces").value);
  const ffmpegPath = byId("ffmpeg-path").value.trim();
  const k17HTTP = {
    enabled: byId("k17-http-enabled").checked,
    listener_address: byId("k17-listener-address").value.trim(),
  };
  if (!locked.has("display_name") && displayName !== current.display_name) intent.display_name = displayName;
  if (!locked.has("pairing_ttl_seconds") && pairingTTL !== current.pairing_ttl_seconds) intent.pairing_ttl_seconds = pairingTTL;
  if (!locked.has("control_origins") && JSON.stringify(origins) !== JSON.stringify(current.control_origins || [])) intent.control_origins = origins;
  if (!locked.has("upnp_interfaces") && JSON.stringify(interfaces) !== JSON.stringify(current.upnp_interfaces || [])) intent.upnp_interfaces = interfaces;
  if (!locked.has("ffmpeg_path") && ffmpegPath !== current.ffmpeg_path) intent.ffmpeg_path = ffmpegPath;
  if (!locked.has("k17_http") && JSON.stringify(k17HTTP) !== JSON.stringify(current.k17_http)) intent.k17_http = k17HTTP;
  return intent;
}

function applyIntent(intent) {
  if (intent.display_name !== undefined) byId("display-name").value = intent.display_name;
  if (intent.pairing_ttl_seconds !== undefined) byId("pairing-ttl").value = text(intent.pairing_ttl_seconds);
  if (intent.control_origins !== undefined) byId("control-origins").value = intent.control_origins.join("\n");
  if (intent.upnp_interfaces !== undefined) byId("upnp-interfaces").value = intent.upnp_interfaces.join("\n");
  if (intent.ffmpeg_path !== undefined) byId("ffmpeg-path").value = intent.ffmpeg_path;
  if (intent.k17_http !== undefined) {
    byId("k17-http-enabled").checked = intent.k17_http.enabled;
    byId("k17-listener-address").value = intent.k17_http.listener_address;
  }
}

async function loadConfig() {
  const result = await requestJSON(routes.config);
  renderConfig(result.body, result.etag);
  return result;
}

function renderRoots() {
  const list = byId("catalog-roots");
  list.replaceChildren();
  if (state.roots.length === 0) {
    list.append(emptyItem("No catalog roots are configured."));
    return;
  }
  for (const root of state.roots) {
    const item = document.createElement("li");
    item.className = "inventory-row";
    item.append(inventoryCopy(root.display_name, `Root ID ${root.root_id}`));
    const scan = document.createElement("button");
    scan.type = "button";
    scan.className = "secondary compact-button";
    scan.textContent = `Scan ${root.display_name}`;
    scan.addEventListener("click", () => startScan(root, scan));
    item.append(scan);
    list.append(item);
  }
}

async function loadRoots() {
  const result = await requestJSON(routes.roots);
  state.roots = result.body.items;
  renderRoots();
}

function rootName(rootID) {
  return state.roots.find((root) => root.root_id === rootID)?.display_name || rootID;
}

function renderJobs() {
  const list = byId("scan-jobs");
  list.replaceChildren();
  if (state.jobs.length === 0) {
    list.append(emptyItem("No scan jobs started in this browser session."));
    return;
  }
  for (const job of state.jobs) {
    const item = document.createElement("li");
    item.className = "inventory-row";
    item.append(inventoryCopy(rootName(job.root_id), `${job.status} · catalog revision ${job.catalog_revision}`));
    list.append(item);
  }
}

async function startScan(root, button) {
  setBusy(button, true, "Starting scan");
  try {
    const result = await requestJSON(routes.scans, { method: "POST", body: JSON.stringify({ root_id: root.root_id }) });
    state.jobs.unshift(result.body);
    renderJobs();
  } catch (error) {
    reportError(error, byId("root-message"));
  } finally {
    setBusy(button, false, "Starting scan");
  }
}

async function refreshJobs() {
  if (state.jobs.length === 0) return;
  const jobs = await Promise.all(state.jobs.map(async (job) => {
    const result = await requestJSON(`${routes.scans}/${encodeURIComponent(job.job_id)}`);
    return result.body;
  }));
  state.jobs = jobs;
  renderJobs();
}

function rendererName(rendererID) {
  if (!rendererID) return "Unassigned";
  return state.renderers.find((renderer) => renderer.renderer_id === rendererID)?.name || rendererID;
}

function renderAudioStatus() {
  if (!state.config) return;
  const k17 = state.renderers.filter((renderer) => renderer.kind === "k17");
  const configuredInterfaces = (state.config.settings.upnp_interfaces || []).length;
  byId("upnp-value").textContent = k17.length ? `${k17.length} K17 observed` : "No K17 observed";
  byId("upnp-detail").textContent = configuredInterfaces
    ? `${configuredInterfaces} UPnP interface selection${configuredInterfaces === 1 ? "" : "s"} configured.`
    : "No explicit UPnP interface selection is configured.";
  const ffmpeg = state.config.settings.ffmpeg_path;
  byId("pcm-value").textContent = ffmpeg ? "Configured" : "Unavailable";
  byId("pcm-detail").textContent = ffmpeg
    ? "An explicit FFmpeg path is configured for PCM fallback."
    : "Configure an explicit FFmpeg path to make PCM fallback available.";
}

function renderRenderers() {
  const list = byId("renderer-inventory");
  list.replaceChildren();
  if (state.renderers.length === 0) {
    list.append(emptyItem("No Renderers have been discovered or paired."));
  } else {
    for (const renderer of state.renderers) {
      const capabilities = renderer.capabilities?.length ? renderer.capabilities.join(", ") : "no reported capabilities";
      const item = document.createElement("li");
      item.className = "inventory-row";
      item.append(inventoryCopy(renderer.name, `${renderer.kind} · ${renderer.status} · ${capabilities}`));
      list.append(item);
    }
  }

  const zones = byId("zones-list");
  zones.replaceChildren();
  if (state.zones.length === 0) {
    zones.append(emptyItem("No zones are configured."));
  } else {
    for (const zone of state.zones) {
      const item = document.createElement("li");
      item.className = "inventory-row";
      item.append(inventoryCopy(zone.name, `${zone.transport} · ${rendererName(zone.renderer_id)}`));
      const actions = document.createElement("div");
      actions.className = "inventory-actions";
      const select = document.createElement("select");
      select.setAttribute("aria-label", `Renderer for ${zone.name}`);
      const empty = document.createElement("option");
      empty.value = "";
      empty.textContent = "Choose Renderer";
      select.append(empty);
      for (const renderer of state.renderers.filter((candidate) => candidate.status !== "revoked")) {
        const option = document.createElement("option");
        option.value = renderer.renderer_id;
        option.textContent = `${renderer.name} (${renderer.status})`;
        option.selected = renderer.renderer_id === zone.renderer_id;
        select.append(option);
      }
      const assign = document.createElement("button");
      assign.type = "button";
      assign.className = "compact-button";
      assign.textContent = `Assign ${zone.name}`;
      assign.addEventListener("click", () => assignRenderer(zone, select.value, assign));
      actions.append(select, assign);
      item.append(actions);
      zones.append(item);
    }
  }
  renderAudioStatus();
}

async function loadRenderers() {
  const result = await requestJSON(routes.zones);
  state.zones = result.body.zones;
  state.renderers = result.body.renderers;
  renderRenderers();
}

async function assignRenderer(zone, rendererID, button) {
  if (!rendererID) {
    setMessage(byId("renderer-message"), `Choose a Renderer for ${zone.name}.`, "error");
    return;
  }
  setBusy(button, true, "Assigning");
  try {
    await requestJSON(`${routes.zones}/${encodeURIComponent(zone.zone_id)}/renderer`, {
      method: "PUT",
      headers: { "If-Match": text(zone.revision), "Idempotency-Key": idempotencyKey("assign") },
      body: JSON.stringify({ renderer_id: rendererID }),
    });
    await loadRenderers();
    setMessage(byId("renderer-message"), `${rendererName(rendererID)} was assigned to ${zone.name}.`, "success");
  } catch (error) {
    reportError(error, byId("renderer-message"));
  } finally {
    setBusy(button, false, "Assigning");
  }
}

function renderDevices() {
  const list = byId("device-list");
  list.replaceChildren();
  if (state.devices.length === 0) {
    list.append(emptyItem("No trusted devices are registered."));
    return;
  }
  for (const device of state.devices) {
    const item = document.createElement("li");
    item.className = "inventory-row";
    item.append(inventoryCopy(device.name, `${device.role} · ${device.revoked ? "revoked" : "active"} · ${device.id}`));
    const revoke = document.createElement("button");
    revoke.type = "button";
    revoke.className = "danger compact-button";
    revoke.textContent = "Revoke";
    revoke.setAttribute("aria-label", `Revoke ${device.name}`);
    revoke.disabled = device.revoked;
    revoke.addEventListener("click", () => revokeDevice(device, revoke));
    item.append(revoke);
    list.append(item);
  }
}

async function loadDevices() {
  const result = await requestJSON(routes.devices);
  state.devices = result.body.devices;
  renderDevices();
}

async function revokeDevice(device, button) {
  setBusy(button, true, "Revoking");
  try {
    await requestJSON(`${routes.devices}/${encodeURIComponent(device.id)}`, { method: "DELETE" });
    setMessage(byId("device-message"), `${device.name} was revoked.`, "success");
    await Promise.all([loadDevices(), loadRenderers()]);
  } catch (error) {
    reportError(error, byId("device-message"));
    if (!byId("admin-shell").hidden) setBusy(button, false, "Revoking");
  }
}

async function loadAll() {
  const [config] = await Promise.all([loadConfig(), loadRoots(), loadRenderers(), loadDevices()]);
  renderJobs();
  return config;
}

byId("login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = event.currentTarget.querySelector("button[type=submit]");
  const token = byId("admin-token").value.trim();
  byId("admin-token").value = "";
  setMessage(byId("login-error"), "");
  setBusy(button, true, "Checking token");
  session.token = token;
  try {
    await loadAll();
    showAdministration();
  } catch (error) {
    session.clear();
    const loginError = byId("login-error");
    reportError(error, loginError);
    loginError.focus();
  } finally {
    setBusy(button, false, "Checking token");
  }
});

byId("settings-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = byId("save-settings");
  setMessage(byId("settings-message"), "");
  byId("conflict-panel").hidden = true;
  for (const control of event.currentTarget.elements) control.removeAttribute?.("aria-invalid");
  state.pendingIntent = captureSettingsIntent();
  if (Object.keys(state.pendingIntent).length === 0) {
    setMessage(byId("settings-message"), "No settings changed.");
    state.pendingIntent = null;
    return;
  }
  setBusy(button, true, "Saving settings");
  try {
    const result = await requestJSON(routes.config, {
      method: "PATCH",
      headers: { "If-Match": state.etag, "Idempotency-Key": idempotencyKey("settings") },
      body: JSON.stringify(state.pendingIntent),
    });
    renderConfig(result.body, result.etag);
    state.pendingIntent = null;
    setMessage(byId("settings-message"), "Settings saved.", "success");
  } catch (error) {
    if (error instanceof APIError && error.code === "STALE_CONFIG_REVISION") {
      byId("conflict-panel").hidden = false;
      byId("conflict-panel").focus();
      setMessage(byId("settings-message"), "Save paused because the settings revision is stale.", "error");
    } else {
      if (error instanceof APIError && error.field) {
        const fieldID = {
          display_name: "display-name",
          pairing_ttl_seconds: "pairing-ttl",
          control_origins: "control-origins",
          upnp_interfaces: "upnp-interfaces",
          ffmpeg_path: "ffmpeg-path",
          k17_http: "k17-listener-address",
        }[error.field];
        if (fieldID) byId(fieldID).setAttribute("aria-invalid", "true");
      }
      reportError(error, byId("settings-message"));
    }
  } finally {
    setBusy(button, false, "Saving settings");
  }
});

byId("refresh-reapply").addEventListener("click", async () => {
  const button = byId("refresh-reapply");
  const intent = state.pendingIntent;
  setBusy(button, true, "Refreshing");
  try {
    await loadConfig();
    if (intent) applyIntent(intent);
    byId("conflict-panel").hidden = true;
    setMessage(byId("settings-message"), "Current settings loaded; your edits were reapplied.");
  } catch (error) {
    reportError(error, byId("settings-message"));
  } finally {
    setBusy(button, false, "Refreshing");
  }
});

byId("reset-settings").addEventListener("click", () => {
  if (state.config) renderConfig(state.config, state.etag);
  state.pendingIntent = null;
  byId("conflict-panel").hidden = true;
  setMessage(byId("settings-message"), "Edits discarded.");
});

byId("root-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const formElement = event.currentTarget;
  const button = formElement.querySelector("button[type=submit]");
  const form = new FormData(formElement);
  setBusy(button, true, "Adding root");
  try {
    await requestJSON(routes.roots, {
      method: "POST",
      body: JSON.stringify({ path: form.get("path").trim(), display_name: form.get("display_name").trim() }),
    });
    formElement.reset();
    setMessage(byId("root-message"), "Catalog root added.", "success");
    await loadRoots();
  } catch (error) {
    reportError(error, byId("root-message"));
  } finally {
    setBusy(button, false, "Adding root");
  }
});

byId("refresh-roots").addEventListener("click", () => loadRoots().catch((error) => reportError(error, byId("root-message"))));
byId("refresh-jobs").addEventListener("click", () => refreshJobs().catch((error) => reportError(error, byId("root-message"))));
byId("refresh-renderers").addEventListener("click", () => loadRenderers()
  .then(() => setMessage(byId("renderer-message"), "Renderer inventory refreshed.", "success"))
  .catch((error) => reportError(error, byId("renderer-message"))));
byId("refresh-devices").addEventListener("click", () => loadDevices().catch((error) => reportError(error, byId("device-message"))));
byId("end-session").addEventListener("click", () => endSession("Session cleared."));
for (const id of sectionIDs) {
  document.querySelector(`.section-nav a[href="#${id}"]`).addEventListener("click", () => setCurrentSection(id));
}
byId("main-content").addEventListener("scroll", scheduleCurrentSection, { passive: true });
window.addEventListener("scroll", scheduleCurrentSection, { passive: true });
window.addEventListener("resize", scheduleCurrentSection);

if (session.token) {
  loadAll().then(showAdministration).catch((error) => reportError(error, byId("login-error")));
} else {
  renderJobs();
}
