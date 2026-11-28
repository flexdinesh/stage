// The harness slot registry: each harness with its own UI registers one entry here (its setup panel, Settings row,
// status reader and policies); every other kind gets the generic entry, rendered from its descriptor's setup schema
// and the generic HarnessStatus. This folder is the only place in the app that names a harness kind
// (ADR 0007 decision 4, tests/boundaries.test.mjs); everything else reads an entry, a descriptor or declared
// capabilities.
import type { Harness } from "../api/types.ts";
import { CLAUDE_SLOTS } from "./claude/index.tsx";
import { CODEX_SLOTS } from "./codex/index.tsx";
import { GENERIC_SLOTS } from "./generic.tsx";
import { OPENCODE_SLOTS } from "./opencode/index.tsx";
import { SESSION_FIXTURE_KIND, SESSION_FIXTURE_SLOTS } from "./sessionFixture.tsx";
import { T3_SLOTS } from "./t3/index.tsx";
import type { ConnectionStatus, HarnessSlots, MachineConnection, SessionSlots } from "./types.ts";

export type { ConnectionRowProps, ConnectionStatus, HarnessSlots, MachineConnection, SessionSlotProps, SessionSlots } from "./types.ts";
export { FIXTURE_ACTIVITY, FIXTURE_DELIVERY } from "./sessionFixture.tsx";

declare global {
  interface ImportMetaEnv {
    DEV: boolean;
  }
  interface ImportMeta {
    readonly env: ImportMetaEnv;
  }
}

// Read lazily: the entries' modules import shared components that import this registry back.
const registered = (): readonly (readonly [Harness, HarnessSlots])[] => [
  ["t3", T3_SLOTS],
  ["opencode", OPENCODE_SLOTS],
  ["codex", CODEX_SLOTS],
  ["claude", CLAUDE_SLOTS],
];

/** A kind's entry, or the generic one. */
export function harnessSlots(kind: Harness): HarnessSlots {
  if (import.meta.env.DEV && kind === SESSION_FIXTURE_KIND) return { ...GENERIC_SLOTS, session: SESSION_FIXTURE_SLOTS };
  return registered().find(([k]) => k === kind)?.[1] ?? GENERIC_SLOTS;
}

export function harnessSessionSlots(kind: Harness): SessionSlots | undefined {
  return harnessSlots(kind).session;
}

/** Every status query key prefix (each entry's, and the generic one for `kinds`), for a refresh after Sync now. */
export function harnessStatusKeys(kinds: readonly Harness[]): (readonly unknown[])[] {
  return kinds.map((k) => harnessSlots(k).statusKey(k));
}

/**
 * Each connection's live status view on its own machine, in input order: its kind's entry, else the generic status.
 * `name` is a kind's display name (generic summaries name it); `poll: false` reads what another observer (the status
 * button) keeps fresh, without a second poll of the same status.
 */
export function useConnectionStatuses<C extends MachineConnection>(
  connections: readonly C[],
  name: (kind: Harness) => string,
  { poll = true }: { poll?: boolean } = {},
): ConnectionStatus<C>[] {
  const every: { refetchInterval?: false } = poll ? {} : { refetchInterval: false };
  const of = (kind: Harness) => connections.filter((c) => c.harness === kind);
  // One status hook per entry, in a fixed order whatever the connections: a line per registered entry.
  const read: ConnectionStatus<C>[] = [
    ...T3_SLOTS.useStatuses(of("t3"), every, name),
    ...OPENCODE_SLOTS.useStatuses(of("opencode"), every, name),
    ...CODEX_SLOTS.useStatuses(of("codex"), every, name),
    ...CLAUDE_SLOTS.useStatuses(of("claude"), every, name),
    ...GENERIC_SLOTS.useStatuses(
      connections.filter((c) => !registered().some(([k]) => k === c.harness)),
      every,
      name,
    ),
  ];
  return connections.flatMap((c) => read.filter((r) => r.connection === c));
}
