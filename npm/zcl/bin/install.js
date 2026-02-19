#!/usr/bin/env node

const fs = require("node:fs");
const path = require("node:path");
const https = require("node:https");

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

function download(url, destPath) {
  return new Promise((resolve, reject) => {
    const file = fs.createWriteStream(destPath);
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
          file.close(() => fs.unlink(destPath, () => {}));
          return resolve(download(res.headers.location, destPath));
        }
        if (res.statusCode !== 200) {
          res.resume();
          file.close(() => fs.unlink(destPath, () => {}));
          return reject(
            new Error(
              `download failed: ${res.statusCode} ${res.statusMessage || ""}`.trim()
            )
          );
        }
        res.pipe(file);
        file.on("finish", () => file.close(resolve));
      }
    );
    req.on("error", (err) => {
      file.close(() => fs.unlink(destPath, () => {}));
      reject(err);
    });
  });
}

async function main() {
  const version = readPackageVersion();
  if (!version || version === "0.0.0-dev") {
    return;
  }

  const { os, arch, ext } = mapTarget();
  const asset = `zcl_${os}_${arch}${ext}`;
  const url = `https://github.com/marcohefti/zero-context-lab/releases/download/v${version}/${asset}`;

  const nativeDir = path.join(__dirname, "..", "native");
  fs.mkdirSync(nativeDir, { recursive: true });
  const outPath = path.join(nativeDir, `zcl${ext}`);

  try {
    await download(url, outPath);
  } catch (err) {
    throw new Error(
      `failed to fetch ${asset} for v${version} from GitHub releases: ${err.message}`
    );
  }

  if (process.platform !== "win32") {
    fs.chmodSync(outPath, 0o755);
  }
}

main().catch((err) => {
  console.error(`zcl postinstall: ${err.message}`);
  process.exit(1);
});
