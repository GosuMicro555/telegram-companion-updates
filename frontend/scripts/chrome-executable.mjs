import { accessSync, constants } from "node:fs";
import path from "node:path";

const browserCommands = ["google-chrome", "google-chrome-stable", "chromium", "chromium-browser"];

export function resolveChromeExecutable(options = {}) {
  const platform = options.platform ?? process.platform;
  const env = options.env ?? process.env;
  const isExecutable = options.isExecutable ?? defaultIsExecutable;
  const override = env.GEOMETRY_CHROME_BIN || env.CHROME_BIN;

  if (override) {
    const resolved = resolveCandidate(override, platform, env, isExecutable);
    if (resolved) return resolved;
    throw new Error(
      `Configured geometry browser is not executable: ${override}. `
      + "Set GEOMETRY_CHROME_BIN to a Chromium-based browser executable."
    );
  }

  for (const candidate of platformCandidates(platform, env)) {
    const resolved = resolveCandidate(candidate, platform, env, isExecutable);
    if (resolved) return resolved;
  }

  throw new Error(
    `No Chromium-based browser executable was found for ${platform}. `
    + "Install Chrome/Chromium or set GEOMETRY_CHROME_BIN to its executable path."
  );
}

export function waitForChromeSpawn(child, executable) {
  return new Promise((resolve, reject) => {
    const cleanup = () => {
      child.removeListener("spawn", onSpawn);
      child.removeListener("error", onError);
    };
    const onSpawn = () => {
      cleanup();
      resolve();
    };
    const onError = (cause) => {
      cleanup();
      const detail = cause instanceof Error ? cause.message : String(cause);
      reject(new Error(`Could not launch geometry browser at ${executable}: ${detail}`, { cause }));
    };

    child.once("spawn", onSpawn);
    child.once("error", onError);
  });
}

function platformCandidates(platform, env) {
  if (platform === "darwin") {
    const home = env.HOME || "";
    return [
      "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
      home && platformJoin(platform, home, "Applications/Google Chrome.app/Contents/MacOS/Google Chrome"),
      "/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
      "/Applications/Chromium.app/Contents/MacOS/Chromium",
      "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
      "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"
    ].filter(Boolean);
  }
  if (platform === "win32") {
    return [
      env.LOCALAPPDATA && platformJoin(platform, env.LOCALAPPDATA, "Google/Chrome/Application/chrome.exe"),
      env.PROGRAMFILES && platformJoin(platform, env.PROGRAMFILES, "Google/Chrome/Application/chrome.exe"),
      env["PROGRAMFILES(X86)"] && platformJoin(platform, env["PROGRAMFILES(X86)"], "Google/Chrome/Application/chrome.exe"),
      env.LOCALAPPDATA && platformJoin(platform, env.LOCALAPPDATA, "Chromium/Application/chrome.exe"),
      env.LOCALAPPDATA && platformJoin(platform, env.LOCALAPPDATA, "Microsoft/Edge/Application/msedge.exe")
    ].filter(Boolean);
  }
  return browserCommands;
}

function resolveCandidate(candidate, platform, env, isExecutable) {
  if (hasPathSeparator(candidate)) return isExecutable(candidate) ? candidate : "";

  const separator = platform === "win32" ? ";" : ":";
  for (const directory of String(env.PATH || "").split(separator).filter(Boolean)) {
    const resolved = platformJoin(platform, directory, candidate);
    if (isExecutable(resolved)) return resolved;
  }
  return "";
}

function platformJoin(platform, ...parts) {
  return platform === "win32" ? path.win32.join(...parts) : path.posix.join(...parts);
}

function hasPathSeparator(candidate) {
  return candidate.includes("/") || candidate.includes("\\");
}

function defaultIsExecutable(candidate) {
  try {
    accessSync(candidate, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}
