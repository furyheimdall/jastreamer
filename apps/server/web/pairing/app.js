const byId = (id) => document.getElementById(id);

const state = { token: sessionStorage.getItem("jstreamer-admin-token") || "" };

async function api(path, options = {}) {
  const headers = { "Content-Type": "application/json", ...(options.headers || {}) };
  if (state.token) headers.Authorization = `Bearer ${state.token}`;
  const response = await fetch(path, { ...options, headers });
  if (response.status === 204) return null;
  const body = await response.json();
  if (!response.ok) throw new Error(`${body.code}: ${body.message}`);
  return body;
}

function message(target, text, error = false) {
  target.textContent = text;
  target.classList.toggle("error", error);
}

async function loadIdentity() {
  try {
    const identity = await api("/api/v1/identity");
    byId("fingerprint").textContent = identity.sha256_fingerprint;
    byId("identity-status").textContent = "Available";
  } catch (error) {
    byId("identity-status").textContent = "Unavailable";
    message(byId("fingerprint"), error.message, true);
  }
}

async function loadDevices() {
  const list = byId("device-list");
  try {
    const result = await api("/api/v1/devices");
    list.replaceChildren();
    if (result.devices.length === 0) {
      const empty = document.createElement("p");
      empty.className = "empty";
      empty.textContent = "No devices are registered.";
      list.append(empty);
      return;
    }
    for (const device of result.devices) {
      const row = document.createElement("div");
      row.className = "device-row";
      const meta = document.createElement("div");
      meta.className = "device-meta";
      const name = document.createElement("strong");
      name.textContent = device.name;
      const detail = document.createElement("p");
      detail.textContent = `${device.role} · ${device.revoked ? "revoked" : "active"} · ${device.id}`;
      meta.append(name, detail);
      const revoke = document.createElement("button");
      revoke.type = "button";
      revoke.className = "danger";
      revoke.textContent = "Revoke";
      revoke.setAttribute("aria-label", `Revoke ${device.name}`);
      revoke.disabled = device.revoked;
      revoke.addEventListener("click", async () => {
        revoke.disabled = true;
        try { await api(`/api/v1/devices/${encodeURIComponent(device.id)}`, { method: "DELETE" }); await loadDevices(); }
        catch (error) { message(byId("session-message"), error.message, true); revoke.disabled = false; }
      });
      row.append(meta, revoke);
      list.append(row);
    }
  } catch (error) {
    list.innerHTML = "";
    const failure = document.createElement("p");
    failure.className = "message error";
    failure.textContent = error.message;
    list.append(failure);
  }
}

byId("bootstrap-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const target = event.currentTarget;
  const form = new FormData(target);
  try {
    const credential = await api("/api/v1/bootstrap", { method: "POST", body: JSON.stringify({ setup_secret: form.get("setupSecret"), name: form.get("name") }) });
    state.token = credential.token;
    sessionStorage.setItem("jstreamer-admin-token", state.token);
    target.reset();
    message(byId("bootstrap-message"), "Administrator created. The token is held only for this browser session.");
    await loadDevices();
  } catch (error) { message(byId("bootstrap-message"), error.message, true); }
});

byId("session-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const target = event.currentTarget;
  state.token = new FormData(target).get("token").trim();
  sessionStorage.setItem("jstreamer-admin-token", state.token);
  message(byId("session-message"), "Session token loaded.");
  await loadDevices();
});

byId("clear-session").addEventListener("click", () => {
  state.token = "";
  sessionStorage.removeItem("jstreamer-admin-token");
  byId("session-form").reset();
  message(byId("session-message"), "Session cleared.");
});

byId("generate-code").addEventListener("click", async () => {
  try {
    const result = await api("/api/v1/pairing-codes", { method: "POST", body: "{}" });
    byId("pairing-code").textContent = result.code;
    message(byId("code-message"), `Expires at ${new Date(result.expires_at).toLocaleTimeString()}.`);
  } catch (error) { message(byId("code-message"), error.message, true); }
});

byId("register-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const target = event.currentTarget;
  const form = new FormData(target);
  try {
    const credential = await api("/api/v1/pairings", { method: "POST", body: JSON.stringify({ code: form.get("code"), name: form.get("name") }) });
    message(byId("register-message"), `Device registered. Save this token now: ${credential.token}`);
    target.reset();
  } catch (error) { message(byId("register-message"), error.message, true); }
});

byId("refresh-devices").addEventListener("click", loadDevices);
loadIdentity();
if (state.token) loadDevices();
