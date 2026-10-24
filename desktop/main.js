"use strict";

// Electron shell for LED Link.
//
// The app owns the lifecycle of the bundled `ledlink` service: it starts the
// Go gateway on a private loopback port, waits until it answers, then shows the
// embedded control interface in a native window. Closing the window stops the
// service, so users never need a terminal.

const { app, BrowserWindow, Menu, dialog, shell } = require("electron");
const { spawn } = require("node:child_process");
const fs = require("node:fs");
const http = require("node:http");
const net = require("node:net");
const path = require("node:path");

const APP_NAME = "adplot LED Link";
const STARTUP_TIMEOUT_MS = 25000;

// AppImages cannot use the bundled SUID sandbox on newer Ubuntu releases
// (AppArmor restricts unprivileged user namespaces), so relax it there. The
// .deb install sets up the sandbox properly and keeps it enabled.
if (process.env.APPIMAGE) {
  app.commandLine.appendSwitch("no-sandbox");
}

let serverProcess = null;
let serverPort = null;
let mainWindow = null;
let logPath = "";
let shuttingDown = false;

function serverBinaryName() {
  return process.platform === "win32" ? "ledlink.exe" : "ledlink";
}

// resolveServerBinary finds the bundled gateway in packaged builds and falls
// back to the repository build output while developing.
function resolveServerBinary() {
  const name = serverBinaryName();
  const candidates = [
    path.join(process.resourcesPath || "", name),
    path.join(__dirname, "..", "dist", "ledlink-linux-amd64"),
    path.join(__dirname, "..", "dist", "ledlink-windows-amd64.exe"),
    path.join(__dirname, "..", "bin", name),
  ];

  for (const candidate of candidates) {
    if (candidate && fs.existsSync(candidate)) return candidate;
  }
  return name; // last resort: rely on PATH
}

function getFreePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });
}

function ping(port) {
  return new Promise((resolve) => {
    const req = http.get(
      { host: "127.0.0.1", port, path: "/api/v1/healthz", timeout: 1000 },
      (res) => {
        res.resume();
        resolve(res.statusCode === 200);
      }
    );
    req.on("error", () => resolve(false));
    req.on("timeout", () => {
      req.destroy();
      resolve(false);
    });
  });
}

async function waitForServer(port) {
  const deadline = Date.now() + STARTUP_TIMEOUT_MS;
  while (Date.now() < deadline) {
    if (await ping(port)) return true;
    if (serverProcess && serverProcess.exitCode !== null) return false;
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  return false;
}

async function startServer() {
  const binary = resolveServerBinary();
  if (!fs.existsSync(binary) && !binary.includes(path.sep)) {
    throw new Error(`bundled LED Link service not found (looked for ${binary})`);
  }

  logPath = path.join(app.getPath("userData"), "ledlink.log");
  fs.mkdirSync(path.dirname(logPath), { recursive: true });
  const logStream = fs.createWriteStream(logPath, { flags: "a" });

  serverPort = await getFreePort();

  serverProcess = spawn(binary, ["serve", "-listen", `127.0.0.1:${serverPort}`], {
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  });

  serverProcess.stdout.pipe(logStream);
  serverProcess.stderr.pipe(logStream);

  serverProcess.on("error", (err) => {
    logStream.write(`\n[launcher] failed to start: ${err.message}\n`);
  });

  serverProcess.on("exit", (code, signal) => {
    serverProcess = null;
    if (shuttingDown) return;
    // The gateway died while the app was running: surface it instead of
    // leaving the user with a broken window.
    if (mainWindow && !mainWindow.isDestroyed()) {
      dialog.showMessageBox(mainWindow, {
        type: "error",
        title: APP_NAME,
        message: "The LED Link service stopped unexpectedly.",
        detail: `Exit code ${code ?? "unknown"}${signal ? ` (${signal})` : ""}.\n\nSee ${logPath} for details.`,
      }).catch(() => {});
    }
  });

  const ready = await waitForServer(serverPort);
  if (!ready) {
    throw new Error(
      `the LED Link service did not respond within ${Math.round(STARTUP_TIMEOUT_MS / 1000)}s`
    );
  }
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1180,
    height: 840,
    minWidth: 900,
    minHeight: 620,
    title: APP_NAME,
    backgroundColor: "#0a0e13",
    icon: path.join(__dirname, "assets", "icon.png"),
    show: false,
    autoHideMenuBar: true,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  mainWindow.once("ready-to-show", () => mainWindow.show());
  mainWindow.loadURL(`http://127.0.0.1:${serverPort}/`);

  // Anything that wants a new window belongs in the user's real browser.
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url).catch(() => {});
    return { action: "deny" };
  });

  mainWindow.on("closed", () => {
    mainWindow = null;
  });
}

function stopServer() {
  shuttingDown = true;
  if (!serverProcess) return;
  try {
    serverProcess.kill();
  } catch {
    // already gone
  }
  serverProcess = null;
}

function shutdown(code = 1) {
  stopServer();
  app.exit(code);
}

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on("second-instance", () => {
    if (!mainWindow) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.focus();
  });

  app.whenReady().then(async () => {
    Menu.setApplicationMenu(null);
    try {
      await startServer();
      createWindow();
    } catch (err) {
      const detail = logPath ? `\n\nLog: ${logPath}` : "";
      dialog.showErrorBox(APP_NAME, `${err.message}${detail}`);
      shutdown(1);
    }
  });

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0 && serverPort) createWindow();
  });

  app.on("window-all-closed", () => shutdown(0));
  app.on("before-quit", stopServer);
  process.on("SIGINT", () => shutdown(0));
  process.on("SIGTERM", () => shutdown(0));
}
