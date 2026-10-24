"use strict";

const POLL_MS = 3000;

const state = {
  devices: [],
  selectedId: null,
  scanning: false,
  busy: false,
};

let lastRenderKey = "";

const $ = (id) => document.getElementById(id);

// ---------------------------------------------------------------- API helper

async function api(path, options = {}) {
  const res = await fetch(path, {
    headers: options.body ? { "Content-Type": "application/json" } : undefined,
    ...options,
  });
  const text = await res.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { data = { raw: text }; }
  }
  if (!res.ok) {
    const message = (data && data.error) || `Request failed (${res.status})`;
    throw new Error(message);
  }
  return data;
}

const devicePath = (id, suffix = "") =>
  `/api/v1/devices/${encodeURIComponent(id)}${suffix}`;

// ---------------------------------------------------------------- feedback UI

let toastTimer = null;

function toast(message, kind = "") {
  const el = $("toast");
  el.textContent = message;
  el.className = `toast show ${kind}`.trim();
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.className = "toast"; }, 3200);
}

function feedback(id, message, kind = "") {
  const el = $(id);
  if (!el) return;
  el.textContent = message;
  el.className = `feedback ${kind}`.trim();
  if (kind === "ok" || kind === "err") {
    setTimeout(() => {
      if (el.textContent === message) el.textContent = "";
    }, 4000);
  }
}

function setBusy(busy) {
  state.busy = busy;
  document.querySelectorAll(".btn").forEach((b) => {
    if (b.id !== "btn-add") b.disabled = busy;
  });
}

// ---------------------------------------------------------------- connection

async function refreshHealth() {
  const pill = $("conn-status");
  try {
    const health = await api("/api/v1/healthz");
    pill.className = "pill pill-online";
    pill.textContent = `Online · v${health.version}`;
  } catch {
    pill.className = "pill pill-offline";
    pill.textContent = "Service unreachable";
  }
}

// ---------------------------------------------------------------- devices

async function loadDevices() {
  try {
    const data = await api("/api/v1/devices");
    state.devices = data.devices || [];
    if (state.selectedId && !state.devices.some((d) => d.id === state.selectedId)) {
      state.selectedId = null;
    }
  } catch {
    // The connection pill already reports outages; polling stays silent.
    state.devices = [];
  }
  // Only touch the DOM when something actually changed, so background polling
  // never interrupts a click or steals focus from a form field.
  if (renderKey() !== lastRenderKey) renderDevices();
}

function renderKey() {
  return JSON.stringify([state.selectedId, state.devices]);
}

function renderDevices() {
  lastRenderKey = renderKey();
  const list = $("device-list");
  const empty = $("empty-devices");
  const count = $("device-count");

  list.replaceChildren();
  count.textContent = String(state.devices.length);
  empty.classList.toggle("hidden", state.devices.length > 0);

  for (const device of state.devices) {
    const card = document.createElement("button");
    card.type = "button";
    card.className = "device-card" + (device.id === state.selectedId ? " selected" : "");
    card.setAttribute("aria-pressed", device.id === state.selectedId ? "true" : "false");

    const dot = document.createElement("span");
    dot.className = "device-dot";

    const main = document.createElement("div");
    main.className = "device-card-main";

    const name = document.createElement("div");
    name.className = "device-card-name";
    name.textContent = device.name || device.id;

    const sub = document.createElement("div");
    sub.className = "device-card-sub";
    sub.textContent = `${device.vendor || device.driver} · ${device.address}`;

    main.append(name, sub);
    card.append(dot, main);
    card.addEventListener("click", () => selectDevice(device.id));
    list.append(card);
  }

  renderPanel();
}

function selectedDevice() {
  return state.devices.find((d) => d.id === state.selectedId) || null;
}

function renderPanel() {
  const device = selectedDevice();
  $("no-selection").classList.toggle("hidden", !!device);
  $("device-panel").classList.toggle("hidden", !device);
  if (!device) return;

  $("sel-name").textContent = device.name || device.id;

  const bits = [`${device.vendor || device.driver}`];
  if (device.model) bits.push(device.model);
  bits.push(device.address);
  if (device.screen_width && device.screen_height) {
    bits.push(`${device.screen_width}×${device.screen_height} px`);
  }
  if (device.firmware) bits.push(`fw ${device.firmware}`);
  $("sel-meta").textContent = bits.join(" · ");

  const chips = $("sel-chips");
  chips.replaceChildren();
  const addChip = (label, accent = false) => {
    const chip = document.createElement("span");
    chip.className = "chip" + (accent ? " chip-accent" : "");
    chip.textContent = label;
    chips.append(chip);
  };
  addChip(device.driver, true);
  if (device.protocol) addChip(device.protocol);
  for (const capability of device.capabilities || []) addChip(capability);
}

function selectDevice(id) {
  state.selectedId = id;
  renderDevices();
}

// ---------------------------------------------------------------- actions

async function sendText() {
  const device = selectedDevice();
  if (!device) return;

  const text = $("text-input").value.trim();
  if (!text) {
    feedback("text-feedback", "Type a message first.", "err");
    $("text-input").focus();
    return;
  }

  const spec = {
    text,
    color: normaliseHex($("color-hex").value),
    effect: $("effect-input").value,
    speed: Number($("speed-input").value),
  };

  setBusy(true);
  feedback("text-feedback", "Sending…");
  try {
    await api(devicePath(device.id, "/text"), {
      method: "POST",
      body: JSON.stringify(spec),
    });
    feedback("text-feedback", "Sent to screen", "ok");
  } catch (err) {
    feedback("text-feedback", err.message, "err");
  } finally {
    setBusy(false);
  }
}

let brightnessTimer = null;

function applyBrightness() {
  clearTimeout(brightnessTimer);
  brightnessTimer = setTimeout(async () => {
    const device = selectedDevice();
    if (!device) return;
    const percent = Number($("brightness-input").value);
    try {
      await api(devicePath(device.id, "/brightness"), {
        method: "POST",
        body: JSON.stringify({ percent }),
      });
      feedback("display-feedback", `Brightness set to ${percent}%`, "ok");
    } catch (err) {
      feedback("display-feedback", err.message, "err");
    }
  }, 220);
}

async function setPower(on) {
  const device = selectedDevice();
  if (!device) return;

  const button = on ? $("btn-power-on") : $("btn-power-off");
  button.disabled = true;
  feedback("display-feedback", on ? "Turning screen on…" : "Turning screen off…");
  try {
    await api(devicePath(device.id, "/power"), {
      method: "POST",
      body: JSON.stringify({ on }),
    });
    feedback("display-feedback", on ? "Screen is on" : "Screen is off", "ok");
  } catch (err) {
    feedback("display-feedback", err.message, "err");
  } finally {
    button.disabled = false;
  }
}

async function disconnectDevice() {
  const device = selectedDevice();
  if (!device) return;
  if (!confirm(`Disconnect “${device.name || device.id}”?`)) return;

  setBusy(true);
  try {
    await api(devicePath(device.id), { method: "DELETE" });
    state.selectedId = null;
    await loadDevices();
    toast("Screen disconnected", "ok");
  } catch (err) {
    toast(err.message, "err");
  } finally {
    setBusy(false);
  }
}

// ---------------------------------------------------------------- scan

async function runScan() {
  if (state.scanning) return;
  state.scanning = true;

  const button = $("btn-scan");
  const results = $("scan-results");
  button.disabled = true;
  feedback("scan-status", "Searching your network…");
  results.replaceChildren(spinnerRow("Scanning — this can take up to a minute"));

  try {
    const targets = $("scan-targets").value.trim() || "local";
    const data = await api("/api/v1/scan", {
      method: "POST",
      body: JSON.stringify({ targets }),
    });
    const found = (data.devices || []).filter((d) => d.reachable);
    feedback("scan-status", `Scanned ${data.probed} address(es), found ${found.length}`, found.length ? "ok" : "");
    renderScanResults(found);
  } catch (err) {
    feedback("scan-status", err.message, "err");
    results.replaceChildren(emptyRow("Scan failed."));
  } finally {
    state.scanning = false;
    button.disabled = false;
  }
}

function renderScanResults(found) {
  const results = $("scan-results");
  results.replaceChildren();
  if (!found.length) {
    results.append(emptyRow("No screens found. Make sure the controller is powered on and on the same network."));
    return;
  }
  for (const item of found) {
    results.append(
      resultRow(item.address, `${item.driver}${item.protocol ? " · " + item.protocol : ""}`, "Connect", async () => {
        await connectDevice(item.address, item.driver);
      })
    );
  }
}

// ---------------------------------------------------------------- identify

async function runIdentify() {
  const address = $("manual-address").value.trim();
  if (!address) {
    feedback("manual-feedback", "Enter an address first.", "err");
    return;
  }

  const results = $("identify-results");
  const button = $("btn-identify");
  button.disabled = true;
  feedback("manual-feedback", "Checking…");
  results.replaceChildren(spinnerRow("Asking each protocol what is at this address"));

  try {
    const data = await api("/api/v1/identify", {
      method: "POST",
      body: JSON.stringify({ address }),
    });
    const matches = data.matches || [];
    results.replaceChildren();
    if (!matches.length) {
      results.append(emptyRow("Nothing answered at this address."));
      feedback("manual-feedback", "No response", "err");
    } else {
      feedback("manual-feedback", `Recognised by ${matches.length} protocol(s)`, "ok");
      for (const match of matches) {
        results.append(resultRow(address, `${match.driver} · ${match.protocol || "unknown"}`, "Connect", async () => {
          await connectDevice(address, match.driver);
        }));
      }
    }
  } catch (err) {
    feedback("manual-feedback", err.message, "err");
    results.replaceChildren(emptyRow("Check failed."));
  } finally {
    button.disabled = false;
  }
}

// ---------------------------------------------------------------- connect

async function connectDevice(address, driver) {
  setBusy(true);
  try {
    const body = { address };
    if (driver && driver !== "auto") body.driver = driver;
    const info = await api("/api/v1/devices", {
      method: "POST",
      body: JSON.stringify(body),
    });
    state.selectedId = info.id;
    await loadDevices();
    closeDialog();
    toast(`Connected to ${info.name || info.id}`, "ok");
  } catch (err) {
    toast(err.message, "err");
  } finally {
    setBusy(false);
  }
}

// ---------------------------------------------------------------- DOM bits

function spinnerRow(label) {
  const row = document.createElement("div");
  row.className = "result";
  const spinner = document.createElement("span");
  spinner.className = "spinner";
  const main = document.createElement("div");
  main.className = "result-main";
  const sub = document.createElement("div");
  sub.className = "result-sub";
  sub.textContent = label;
  main.append(sub);
  row.append(spinner, main);
  return row;
}

function emptyRow(label) {
  const row = document.createElement("div");
  row.className = "result-empty";
  row.textContent = label;
  return row;
}

function resultRow(address, detail, buttonLabel, onClick) {
  const row = document.createElement("div");
  row.className = "result";

  const main = document.createElement("div");
  main.className = "result-main";
  const addr = document.createElement("div");
  addr.className = "result-addr";
  addr.textContent = address;
  const sub = document.createElement("div");
  sub.className = "result-sub";
  sub.textContent = detail;
  main.append(addr, sub);

  const button = document.createElement("button");
  button.type = "button";
  button.className = "btn btn-primary btn-small";
  button.textContent = buttonLabel;
  button.addEventListener("click", async () => {
    button.disabled = true;
    button.textContent = "Connecting…";
    await onClick();
    button.disabled = false;
    button.textContent = buttonLabel;
  });

  row.append(main, button);
  return row;
}

function normaliseHex(value) {
  const hex = (value || "").trim();
  return /^#[0-9a-fA-F]{6}$/.test(hex) ? hex.toLowerCase() : "#00ff00";
}

// ---------------------------------------------------------------- dialog

function openDialog() {
  $("add-dialog").showModal();
  $("scan-results").replaceChildren();
  $("identify-results").replaceChildren();
  feedback("scan-status", "");
  feedback("manual-feedback", "");
}

function closeDialog() {
  $("add-dialog").close();
}

function switchTab(name) {
  document.querySelectorAll(".tab").forEach((tab) => {
    tab.classList.toggle("active", tab.dataset.tab === name);
  });
  document.querySelectorAll(".tab-panel").forEach((panel) => {
    panel.classList.toggle("hidden", panel.dataset.panel !== name);
  });
}

async function loadDrivers() {
  try {
    const data = await api("/api/v1/drivers");
    const select = $("manual-driver");
    for (const name of data.drivers || []) {
      const option = document.createElement("option");
      option.value = name;
      option.textContent = name;
      select.append(option);
    }
  } catch {
    // the selector keeps its "Detect automatically" default
  }
}

// ---------------------------------------------------------------- wiring

function bind() {
  $("btn-add").addEventListener("click", openDialog);
  $("btn-close-dialog").addEventListener("click", closeDialog);
  $("btn-scan").addEventListener("click", runScan);
  $("btn-identify").addEventListener("click", runIdentify);
  $("btn-connect-manual").addEventListener("click", () => {
    const address = $("manual-address").value.trim();
    if (!address) {
      feedback("manual-feedback", "Enter an address first.", "err");
      return;
    }
    connectDevice(address, $("manual-driver").value);
  });
  $("btn-send-text").addEventListener("click", sendText);
  $("btn-disconnect").addEventListener("click", disconnectDevice);
  $("btn-power-on").addEventListener("click", () => setPower(true));
  $("btn-power-off").addEventListener("click", () => setPower(false));

  document.querySelectorAll(".tab").forEach((tab) => {
    tab.addEventListener("click", () => switchTab(tab.dataset.tab));
  });

  $("color-input").addEventListener("input", (e) => {
    $("color-hex").value = e.target.value.toLowerCase();
  });
  $("color-hex").addEventListener("input", (e) => {
    const value = e.target.value.trim();
    if (/^#[0-9a-fA-F]{6}$/.test(value)) $("color-input").value = value.toLowerCase();
  });

  $("speed-input").addEventListener("input", (e) => {
    $("speed-out").textContent = e.target.value;
  });
  $("brightness-input").addEventListener("input", (e) => {
    $("brightness-out").textContent = `${e.target.value}%`;
  });
  $("brightness-input").addEventListener("change", applyBrightness);

  // Ctrl/Cmd+Enter sends the message from anywhere in the form.
  $("text-input").addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      sendText();
    }
  });

  document.addEventListener("click", (e) => {
    const dialog = $("add-dialog");
    if (e.target === dialog) dialog.close();
  });
}

async function init() {
  bind();
  await refreshHealth();
  await Promise.all([loadDevices(), loadDrivers()]);
  setInterval(loadDevices, POLL_MS);
  setInterval(refreshHealth, 15000);
}

document.addEventListener("DOMContentLoaded", init);
