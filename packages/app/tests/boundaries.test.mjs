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

// ADR 0007 decision 4: the UI reads a harness only through what it declares (descriptor, capabilities, launch
// options) and its slot registry entry; only src/harnesses/ names a harness kind.
const HARNESSES = join("harnesses") + "/";
const KIND_LITERAL = /(["'`])(t3|opencode|codex|claude)\1/;
const NAME_COMPARISON = /(===|!==|==|!=|\bcase)\s*(["'`])(T3|OpenCode|Codex|Claude Code)\2/;
const KIND_IMPORT = /from\s+["'][./]*harnesses\/(?!index\.ts["'])[^"']+["']/;

/** Harness-kind literals, display-name comparisons and imports past the registry in `text`, as findings for `rel`. */
export function harnessKindFindings(rel, text) {
  const found = [];
  text.split("\n").forEach((line, i) => {
    if (/^\s*(\/\/|\*|\/\*)/.test(line)) return;
    if (KIND_LITERAL.test(line)) found.push(`${rel}:${i + 1}: harness kind literal (go through src/harnesses/index.ts): ${line.trim()}`);
    if (NAME_COMPARISON.test(line)) found.push(`${rel}:${i + 1}: harness name comparison (read a capability or the registry): ${line.trim()}`);
    if (KIND_IMPORT.test(line)) found.push(`${rel}:${i + 1}: import a harness through src/harnesses/index.ts: ${line.trim()}`);
  });
  return found;
}

test("only src/harnesses/ names a harness kind", () => {
  const found = [];
  for (const file of files(src)) {
    const rel = relative(src, file);
    if (rel.startsWith(HARNESSES)) continue;
    found.push(...harnessKindFindings(rel, readFileSync(file, "utf8")));
  }
  assert.deepEqual(found, []);
});

test("the harness boundary check catches a planted kind literal, name comparison and per-kind import", () => {
  assert.equal(harnessKindFindings("x.ts", `if (c.harness === "t3") return;`).length, 1);
  assert.equal(harnessKindFindings("x.ts", "const k = `claude`;").length, 1);
  assert.equal(harnessKindFindings("x.ts", `if (name === "OpenCode") return;`).length, 1);
  assert.equal(harnessKindFindings("x.ts", `import { T3_KIND } from "../harnesses/t3/api.ts";`).length, 1);
  assert.deepEqual(harnessKindFindings("x.ts", `// the "t3" harness\nimport { harnessSlots } from "../harnesses/index.ts";`), []);
});
