import { spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { extname, join, resolve, sep } from "node:path";
import { build } from "vite";
import { resolveChromeExecutable, waitForChromeSpawn } from "./chrome-executable.mjs";

const root = resolve(import.meta.dirname, "..");
const chromeExecutable = resolveChromeExecutable();
const output = mkdtempSync(join(tmpdir(), "task12-analytics-geometry-"));
const screenshots = join(output, "screenshots");
const bundle = join(output, "bundle");
mkdirSync(screenshots);
await build({
  root,
  base: "./",
  configFile: false,
  logLevel: "silent",
  build: {
    outDir: bundle,
    emptyOutDir: true,
    rollupOptions: { input: join(root, "analytics-geometry.html") }
  }
});

const geometryServer = createServer((request, response) => {
  const pathname = new URL(request.url || "/", "http://127.0.0.1").pathname;
  const requested = pathname === "/" ? "/analytics-geometry.html" : pathname;
  const filePath = resolve(bundle, `.${decodeURIComponent(requested)}`);
  if (!filePath.startsWith(`${resolve(bundle)}${sep}`)) {
    response.writeHead(403).end();
    return;
  }
  let body;
  try {
    body = readFileSync(filePath);
  } catch {
    response.writeHead(404).end();
    return;
  }
  const contentTypes = { ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8" };
  response.writeHead(200, { "Content-Type": contentTypes[extname(filePath)] || "application/octet-stream" });
  response.end(body);
});
await new Promise((resolveListen, reject) => {
  geometryServer.once("error", reject);
  geometryServer.listen(0, "127.0.0.1", resolveListen);
});
const serverAddress = geometryServer.address();
if (!serverAddress || typeof serverAddress === "string") throw new Error("geometry server did not expose a TCP port");
const pageURL = `http://127.0.0.1:${serverAddress.port}/analytics-geometry.html`;

const initialScales = process.env.GEOMETRY_SCALE ? [Number(process.env.GEOMETRY_SCALE)] : [80, 90, 100, 110, 125, 150];
const conditionalScales = [100, 150];
const states = ["initial", "neutral", "positive", "negative"];
const viewports = [
  { name: "native-launch", width: 980, height: 700, dpr: 1 },
  { name: "fullhd", width: 1920, height: 1080, dpr: 1 },
  { name: "large", width: 2880, height: 1864, dpr: 1 },
  { name: "retina-3456x2234", width: 1728, height: 1117, dpr: 2 }
].filter((viewport) => !process.env.GEOMETRY_VIEWPORT || viewport.name === process.env.GEOMETRY_VIEWPORT);

const cases = [];
for (const viewport of viewports) {
  for (const scale of initialScales) cases.push({ viewport, scale, state: "initial" });
  if (viewport.name !== "native-launch") {
    for (const state of states.slice(1)) {
      for (const scale of conditionalScales) cases.push({ viewport, scale, state });
    }
  }
}
const selectedCases = cases.filter((entry) =>
  (!process.env.GEOMETRY_STATE || entry.state === process.env.GEOMETRY_STATE)
  && (!process.env.GEOMETRY_SCALE || entry.scale === Number(process.env.GEOMETRY_SCALE))
);

const results = [];
try {
  for (const entry of selectedCases) {
    const { viewport, scale, state } = entry;
    process.stdout.write(`geometry case: ${viewport.name} ${scale}% ${state}\n`);
    const screenshotPath = join(screenshots, `${viewport.name}-${scale}-${state}.png`);
    const url = `${pageURL}?state=${encodeURIComponent(state)}&scale=${scale}`;
    const captured = await runCDPCase(url, viewport, screenshotPath, `${viewport.name} ${scale} ${state}`);
    const result = { viewport, scale, state, screenshotPath, domLength: captured.domLength, ...captured.geometry };
    results.push(result);
    if (!result.pass) throw new Error(`analytics geometry failed: ${JSON.stringify(result)}`);
  }
} finally {
  await new Promise((resolveClose) => geometryServer.close(resolveClose));
}

writeFileSync(join(output, "results.json"), JSON.stringify(results, null, 2));
process.stdout.write(`${JSON.stringify({ output, cases: results.length, pass: true })}\n`);

async function runCDPCase(url, viewport, screenshotPath, label) {
  const profile = mkdtempSync(join(tmpdir(), "task12-geometry-chrome-"));
  const debuggingPort = await reservePort();
  const chrome = spawn(chromeExecutable, [
    "--headless=new", "--disable-gpu", "--no-sandbox", "--hide-scrollbars",
    "--disable-background-networking", "--disable-component-update", "--disable-default-apps",
    "--disable-sync", "--metrics-recording-only", "--no-first-run",
    `--remote-debugging-port=${debuggingPort}`,
    `--user-data-dir=${profile}`,
    `--window-size=${viewport.width},${viewport.height}`,
    `--force-device-scale-factor=${viewport.dpr}`,
    url
  ], { detached: true, stdio: ["ignore", "ignore", "pipe"] });
  let browser;
  let page;
  try {
    await waitForChromeSpawn(chrome, chromeExecutable);
    return await withTimeout((async () => {
      const debuggerURL = await waitForDebuggerURL(debuggingPort);
      const targets = await waitForTargets(`http://127.0.0.1:${debuggingPort}/json/list`);
      const target = targets.find((entry) => entry.type === "page");
      if (!target?.webSocketDebuggerUrl) throw new Error("Chrome page target was not available");
      browser = await connectCDP(debuggerURL);
      page = await connectCDP(target.webSocketDebuggerUrl);
      await page.send("Page.enable");
      await page.send("Runtime.enable");
      const encoded = await waitForGeometry(page);
      const geometry = JSON.parse(decodeURIComponent(encoded));
      const dom = await page.send("Runtime.evaluate", {
        expression: "document.documentElement.outerHTML",
        returnByValue: true
      });
      const screenshot = await page.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
      writeFileSync(screenshotPath, screenshot.data, "base64");
      return { geometry, domLength: dom.result.value.length };
    })(), 12_000, `CDP geometry timed out for ${label}`);
  } finally {
    page?.close();
    if (browser) {
      try { await withTimeout(browser.send("Browser.close"), 300, "Chrome close timed out"); } catch { /* process group cleanup follows */ }
      browser.close();
    }
    await terminateChromeGroup(chrome);
    rmSync(profile, { recursive: true, force: true });
  }
}

async function reservePort() {
  const reservation = createServer();
  await new Promise((resolveListen, reject) => {
    reservation.once("error", reject);
    reservation.listen(0, "127.0.0.1", resolveListen);
  });
  const address = reservation.address();
  if (!address || typeof address === "string") throw new Error("could not reserve a Chrome debugging port");
  await new Promise((resolveClose) => reservation.close(resolveClose));
  return address.port;
}

async function waitForDebuggerURL(port) {
  for (let attempt = 0; attempt < 200; attempt++) {
    try {
      const response = await fetch(`http://127.0.0.1:${port}/json/version`);
      if (response.ok) {
        const version = await response.json();
        if (version.webSocketDebuggerUrl) return version.webSocketDebuggerUrl;
      }
    } catch {
      // The fixed local port is reserved before launch and opens asynchronously.
    }
    await delay(25);
  }
  throw new Error(`Chrome debugger endpoint did not open on port ${port}`);
}

async function terminateChromeGroup(chrome) {
  try { process.kill(-chrome.pid, "SIGTERM"); } catch { /* already stopped */ }
  await delay(250);
  try { process.kill(-chrome.pid, "SIGKILL"); } catch { /* process group is gone */ }
  if (chrome.exitCode === null) {
    await Promise.race([new Promise((resolveExit) => chrome.once("exit", resolveExit)), delay(500)]);
  }
}

async function waitForTargets(endpoint) {
  for (let attempt = 0; attempt < 80; attempt++) {
    try {
      const response = await fetch(endpoint);
      if (response.ok) {
        const targets = await response.json();
        if (targets.some((entry) => entry.type === "page")) return targets;
      }
    } catch {
      // Chrome may expose the websocket before the target list is ready.
    }
    await delay(25);
  }
  throw new Error("Chrome page target list was not ready");
}

async function waitForGeometry(page) {
  for (let attempt = 0; attempt < 160; attempt++) {
    const result = await page.send("Runtime.evaluate", {
      expression: "JSON.stringify({ geometry: document.body?.dataset.geometry || '', error: document.body?.dataset.geometryError || '' })",
      returnByValue: true
    });
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.text || "geometry page evaluation failed");
    const state = JSON.parse(result.result.value || "{}");
    if (state.error) throw new Error(`geometry page failed: ${state.error}`);
    if (state.geometry) return state.geometry;
    await delay(25);
  }
  throw new Error("geometry page did not publish a result");
}

async function connectCDP(url) {
  const socket = new WebSocket(url);
  await new Promise((resolveOpen, reject) => {
    socket.addEventListener("open", resolveOpen, { once: true });
    socket.addEventListener("error", () => reject(new Error(`CDP websocket failed: ${url}`)), { once: true });
  });
  let nextID = 0;
  const pending = new Map();
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    if (!message.id || !pending.has(message.id)) return;
    const operation = pending.get(message.id);
    pending.delete(message.id);
    if (message.error) operation.reject(new Error(message.error.message));
    else operation.resolve(message.result);
  });
  socket.addEventListener("close", () => {
    for (const operation of pending.values()) operation.reject(new Error("CDP websocket closed"));
    pending.clear();
  });
  return {
    send(method, params = {}) {
      const id = ++nextID;
      return new Promise((resolveResult, reject) => {
        pending.set(id, { resolve: resolveResult, reject });
        socket.send(JSON.stringify({ id, method, params }));
      });
    },
    close() { socket.close(); }
  };
}

function withTimeout(promise, milliseconds, message) {
  let timer;
  const timeout = new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(message)), milliseconds); });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

function delay(milliseconds) {
  return new Promise((resolveDelay) => setTimeout(resolveDelay, milliseconds));
}
