// The shared app reaches the host only through its platform contract
// (src/platform/index.ts). These browser APIs belong in a host adapter
// (apps/web/src/platform/), and no host framework may leak into product code.
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { test } from "node:test";

const src = new URL("../src/", import.meta.url).pathname;

const FORBIDDEN = [
  [/\bEventSource\b/, "transport.subscribe"],
  [/\bfetch\(/, "transport.request (via useApi)"],
  [/["'`]\/api\//, "API-relative paths through the transport"],
  [/\blocalStorage\b|\bsessionStorage\b/, "platform.storage"],
  [/navigator\.clipboard|execCommand\(\s*["']copy["']/, "platform.clipboard"],
  [/\bserviceWorker\b|\bPushManager\b|\bNotification\.(permission|requestPermission)\b/, "platform.notifications"],
  [/setAppBadge|clearAppBadge|rel="icon"/, "platform.attention"],
  [/location\.(origin|assign)|window\.open\(/, "platform.links"],
  [/\bisSecureContext\b|navigator\.userAgent/, "a host adapter"],
  [/from\s+["'](mygo|mygo-runtime|@mygo-plugins\/[^"']*)["']/, "the desktop host"],
];

function files(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? files(join(dir, e.name)) : /\.(ts|tsx)$/.test(e.name) ? [join(dir, e.name)] : [],
  );
}

test("product code uses the platform contract, not browser or host APIs", () => {
  const found = [];
  for (const file of files(src)) {
    const rel = relative(src, file);
    if (rel === join("platform", "index.ts")) continue; // the contract's own docs name what it abstracts
    readFileSync(file, "utf8")
      .split("\n")
      .forEach((line, i) => {
        if (/^\s*(\/\/|\*)/.test(line)) return;
        for (const [pattern, use] of FORBIDDEN) if (pattern.test(line)) found.push(`${rel}:${i + 1}: use ${use}: ${line.trim()}`);
      });
  }
  assert.deepEqual(found, []);
});

test("every dropdown is the Combobox, never a native select", () => {
  const found = [];
  for (const file of files(src)) {
    readFileSync(file, "utf8")
      .split("\n")
      .forEach((line, i) => {
        if (/<select\b/.test(line)) found.push(`${relative(src, file)}:${i + 1}: use Combobox: ${line.trim()}`);
      });
  }
  assert.deepEqual(found, []);
});
