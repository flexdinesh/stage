// Loading placeholders go through the shared skeleton in components/ui.tsx: SkeletonBar pulses only without
// reduced motion, and Skeleton holds its space hidden for 200ms so fast loads show nothing. A raw pulse skips both.
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { test } from "node:test";

const src = new URL("../src/", import.meta.url).pathname;
const OWNER = join("components", "ui.tsx");

function files(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? files(join(dir, e.name)) : /\.(ts|tsx)$/.test(e.name) ? [join(dir, e.name)] : [],
  );
}

test("loading placeholders use SkeletonBar and Skeleton, not a raw pulse", () => {
  const found = [];
  for (const file of files(src)) {
    const rel = relative(src, file);
    if (rel === OWNER) continue;
    readFileSync(file, "utf8")
      .split("\n")
      .forEach((line, i) => {
        if (/\banimate-pulse\b/.test(line)) found.push(`${rel}:${i + 1}: use SkeletonBar (inside Skeleton) from components/ui.tsx: ${line.trim()}`);
      });
  }
  assert.deepEqual(found, []);
});

test("the shared bar pulses only when motion is allowed", () => {
  const ui = readFileSync(join(src, OWNER), "utf8");
  const pulses = ui.match(/[\w:-]*animate-pulse\b/g) ?? [];
  assert.ok(pulses.length > 0, "SkeletonBar pulses");
  assert.deepEqual(
    pulses.filter((p) => p !== "motion-safe:animate-pulse"),
    [],
    "every pulse in ui.tsx is gated with motion-safe:",
  );
});
