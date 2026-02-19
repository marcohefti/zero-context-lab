#!/usr/bin/env node

const fs = require("node:fs");
const path = require("node:path");
const https = require("node:https");
const crypto = require("node:crypto");

function readPackageVersion() {
  const pkgPath = path.join(__dirname, "..", "package.json");
  const pkg = JSON.parse(fs.readFileSync(pkgPath, "utf8"));
  return String(pkg.version || "").trim();
}

function mapTarget() {
  let os = "";
  if (process.platform === "darwin") os = "darwin";
  else if (process.platform === "linux") os = "linux";
  else if (process.platform === "win32") os = "windows";
  else throw new Error(`unsupported platform: ${process.platform}`);

  let arch = "";
  if (process.arch === "arm64") arch = "arm64";
  else if (process.arch === "x64") arch = "amd64";
  else throw new Error(`unsupported arch: ${process.arch}`);

  const ext = os === "windows" ? ".exe" : "";
  return { os, arch, ext };
}

function fetchBuffer(url) {
  return new Promise((resolve, reject) => {
    const req = https.get(
      url,
      { headers: { "User-Agent": "zcl-npm-wrapper" } },
      (res) => {
        if (
          res.statusCode &&
          res.statusCode >= 300 &&
          res.statusCode < 400 &&
          res.headers.location
        ) {
          res.resume();
          return resolve(fetchBuffer(res.headers.location));
        }
        if (res.statusCode !== 200) {
          res.resume();
          return reject(
            new Error(
              `download failed: ${res.statusCode} ${res.statusMessage || ""}`.trim()
            )
          );
        }
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => resolve(Buffer.concat(chunks)));
        res.on("error", reject);
      }
    );
    req.on("error", (err) => {
      reject(err);
    });
  });
}

function parseSha256Sums(text, assetName) {
  const lines = String(text || "").split(/\r?\n/);
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const match = /^([a-fA-F0-9]{64})\s+\*?(.+)$/.exec(trimmed);
    if (!match) continue;
    if (match[2].trim() === assetName) {
      return match[1].toLowerCase();
    }
  }
  return "";
}

function sha256Hex(buf) {
  return crypto.createHash("sha256").update(buf).digest("hex");
}

async function main() {
  const version = readPackageVersion();
  if (!version || version === "0.0.0-dev") {
    return;
  }

  const { os, arch, ext } = mapTarget();
  const asset = `zcl_${os}_${arch}${ext}`;
  const releaseBase = `https://github.com/marcohefti/zero-context-lab/releases/download/v${version}`;
  const url = `${releaseBase}/${asset}`;
  const sumsUrl = `${releaseBase}/SHA256SUMS`;

  const nativeDir = path.join(__dirname, "..", "native");
  fs.mkdirSync(nativeDir, { recursive: true });
  const outPath = path.join(nativeDir, `zcl${ext}`);

  let expectedSha = "";
  try {
    const sums = await fetchBuffer(sumsUrl);
    expectedSha = parseSha256Sums(sums.toString("utf8"), asset);
  } catch (err) {
    throw new Error(
      `failed to fetch SHA256SUMS for v${version} from GitHub releases: ${err.message}`
    );
  }
  if (!expectedSha) {
    throw new Error(`SHA256SUMS missing entry for ${asset} at v${version}`);
  }

  let bin;
  try {
    bin = await fetchBuffer(url);
  } catch (err) {
    throw new Error(
      `failed to fetch ${asset} for v${version} from GitHub releases: ${err.message}`
    );
  }
  const gotSha = sha256Hex(bin);
  if (gotSha !== expectedSha) {
    throw new Error(
      `checksum mismatch for ${asset}: expected ${expectedSha}, got ${gotSha}`
    );
  }

  fs.writeFileSync(outPath, bin);

  if (process.platform !== "win32") {
    fs.chmodSync(outPath, 0o755);
  }
}

main().catch((err) => {
  console.error(`zcl postinstall: ${err.message}`);
  process.exit(1);
});
