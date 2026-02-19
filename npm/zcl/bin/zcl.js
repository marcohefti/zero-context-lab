#!/usr/bin/env node

const { spawnSync } = require("node:child_process");
const path = require("node:path");
const fs = require("node:fs");

function resolveBin() {
  const ext = process.platform === "win32" ? ".exe" : "";
  const p = path.join(__dirname, "..", "native", `zcl${ext}`);
  if (fs.existsSync(p)) return p;

  if (process.env.ZCL_BIN_PATH && fs.existsSync(process.env.ZCL_BIN_PATH)) {
    return process.env.ZCL_BIN_PATH;
  }
  return null;
}

const bin = resolveBin();
if (!bin) {
  console.error("zcl: missing native binary (postinstall may have failed)");
  console.error("zcl: try reinstalling: npm i -g @marcohefti/zcl");
  process.exit(1);
}

const res = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
process.exit(res.status == null ? 1 : res.status);
