// 从反代 device.js / cursor-agent-env.js 原样摘出的实现，用来给 Go 侧生成对拍向量。
// 不参与构建，仅在需要重新生成 device_test.go 的固定向量时手动运行：
//   node checksum_reference.js
const crypto = require("node:crypto");

function sha256Hex(input) {
  return crypto.createHash("sha256").update(String(input)).digest("hex");
}

function deriveTelemetryIds(seed) {
  const base = sha256Hex(`cursor-proxy:v1:${seed || "anonymous"}`);
  return {
    machineId: sha256Hex(`machine:${base}`),
    macMachineId: sha256Hex(`mac:${base}`) + sha256Hex(`mac2:${base}`)
  };
}

function wZg(bytes) {
  const e = Uint8Array.from(bytes);
  let t = 165;
  for (let n = 0; n < e.length; n++) {
    e[n] = (e[n] ^ t) + (n % 256);
    t = e[n];
  }
  return e;
}

function b64url(bytes) {
  const A = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
  let r = "";
  const e = bytes;
  const s = e.byteLength % 3;
  let o = 0;
  for (; o < e.byteLength - s; o += 3) {
    const a = e[o];
    const c = e[o + 1];
    const l = e[o + 2];
    r += A[a >>> 2] + A[((a << 4) | (c >>> 4)) & 63] + A[((c << 2) | (l >>> 6)) & 63] + A[l & 63];
  }
  if (s === 1) r += A[e[o] >>> 2] + A[(e[o] << 4) & 63];
  else if (s === 2)
    r += A[e[o] >>> 2] + A[((e[o] << 4) | (e[o + 1] >>> 4)) & 63] + A[(e[o + 1] << 2) & 63];
  return r;
}

function checksumFor(nowMs, machineId, macMachineId) {
  const E = Math.floor(nowMs / 1e6);
  const x = new Uint8Array([
    (E >> 40) & 255,
    (E >> 32) & 255,
    (E >> 24) & 255,
    (E >> 16) & 255,
    (E >> 8) & 255,
    E & 255
  ]);
  return { bytes: Array.from(x), obfuscated: Array.from(wZg(x)), checksum: `${b64url(wZg(x))}${machineId}/${macMachineId}` };
}

const seeds = ["", "anonymous", "account-42", "eyJhbGciOiJIUzI1NiJ9.payload.sig"];
const timestamps = [0, 1, 1000000, 1769000000000, 1774519200000, 2000000000000];

const out = { telemetry: {}, checksum: {} };
for (const seed of seeds) out.telemetry[seed] = deriveTelemetryIds(seed);
const ids = deriveTelemetryIds("account-42");
for (const ms of timestamps) out.checksum[ms] = checksumFor(ms, ids.machineId, ids.macMachineId);

console.log(JSON.stringify(out, null, 2));
