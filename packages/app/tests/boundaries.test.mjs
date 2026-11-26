// The shared app reaches the host only through its platform contract
// (src/platform/index.ts). These browser APIs belong in a host adapter
// (apps/web/src/platform/), and no host framework may leak into product code.
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, relative, sep } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const src = fileURLToPath(new URL("../src/", import.meta.url));

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
const HARNESSES = "harnesses" + sep;
const KIND_IMPORT = /from\s+["'][./]*harnesses\/(?!index\.ts["'])[^"']+["']/;

function subdirectories(dir) {
  return readdirSync(dir, { withFileTypes: true }).filter((entry) => entry.isDirectory()).map((entry) => entry.name);
}

function descriptorNames() {
  // Display names come from the Go descriptors, independently of the UI slot directory names.
  const internal = fileURLToPath(new URL("../../../server/internal/", import.meta.url));
  return subdirectories(internal).flatMap((dir) => {
    const file = join(internal, dir, "descriptor.go");
    if (!existsSync(file)) return [];
    const name = readFileSync(file, "utf8").match(/\bDescriptor\s*=\s*harness\.Descriptor\s*\{[^}]*?\bName:\s*"([^"\n]+)"/)?.[1];
    return name ? [name] : [];
  });
}

function harnessBoundaryFor(kinds, names) {
  const kind = kinds.map(RegExp.escape).join("|") || "(?!)";
  const name = [...new Set([...kinds, ...names])].map(RegExp.escape).join("|") || "(?!)";
  const quote = `(["'\x60])`;
  const compare = "(?:===|!==|==|!=)";
  return {
    kindLiteral: new RegExp(`${quote}(?:${kind})\\1`),
    nameComparison: new RegExp(`(?:${compare}|\\bcase)\\s*${quote}(?:${name})\\1|${quote}(?:${name})\\2\\s*${compare}`, "i"),
  };
}

const harnessBoundary = harnessBoundaryFor(subdirectories(join(src, "harnesses")), descriptorNames());

/** Harness-kind literals, display-name comparisons and imports past the registry in `text`, as findings for `rel`. */
export function harnessKindFindings(rel, text, { kindLiteral, nameComparison } = harnessBoundary) {
  const found = [];
  const code = text.replace(
    /(["'`])(?:\\[\s\S]|(?!\1)[^\\])*\1|\/\/[^\n]*|\/\*[\s\S]*?\*\//g,
    (token, quote) => quote ? token : token.replace(/[^\n]/g, " "),
  );
  code.split("\n").forEach((line, i) => {
    if (kindLiteral.test(line)) found.push(`${rel}:${i + 1}: harness kind literal (go through src/harnesses/index.ts): ${line.trim()}`);
    else if (nameComparison.test(line)) found.push(`${rel}:${i + 1}: harness name comparison (read a capability or the registry): ${line.trim()}`);
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
  assert.equal(harnessKindFindings("x.ts", `if (name === "Claude Code") return;`).length, 1);
  assert.equal(harnessKindFindings("x.ts", `import { T3_KIND } from "../harnesses/t3/api.ts";`).length, 1);
  assert.deepEqual(harnessKindFindings("x.ts", `const kind = c.harness; // the "t3" harness
/*
  The name "Claude Code" and kind "claude" can appear in a comment.
*/
import { harnessSlots } from "../harnesses/index.ts";`), []);
});

test("the harness boundary recognizes a new slot directory and display name", (t) => {
  const slots = mkdtempSync(join(tmpdir(), "stage-harness-boundary-"));
  t.after(() => rmSync(slots, { recursive: true }));
  mkdirSync(join(slots, "drift.wave"));
  const boundary = harnessBoundaryFor(subdirectories(slots), ["Drift Wave"]);
  const check = (text) => harnessKindFindings("x.ts", text, boundary);
  assert.match(check(`const k = "drift.wave";`)[0], /harness kind literal/);
  assert.match(check(`if ("Drift Wave" === name) return;`)[0], /harness name comparison/);
  assert.deepEqual(check(`const k = "drift-wave"; const label = "Drift Wave";`), []);
});
