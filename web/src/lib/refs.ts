/**
 * Stage links: copyable references to tasks and sessions, and recognizing them in text. Agents resolve the same
 * links with `read_ref` (server/internal/refs); the host is ignored there and here, because one stage is reached
 * at several addresses (loopback, tailnet, phone).
 */

export type StageRef = { kind: "task"; id: number } | { kind: "session"; id: string };

export const taskUrl = (origin: string, id: number) => `${origin}/tasks/${id}`;
export const sessionUrl = (origin: string, threadId: string) => `${origin}/sessions/${encodeURIComponent(threadId)}`;

/** Escape text for a markdown link label. */
const label = (text: string) => text.replace(/[\\[\]]/g, (c) => `\\${c}`).replace(/\s+/g, " ").trim();

/** `[stage task #12: Title](url)`: names itself, so an agent knows what it is even without stage's instructions. */
export const taskMarkdown = (origin: string, id: number, title: string) =>
  `[stage task #${id}: ${label(title)}](${taskUrl(origin, id)})`;

export const sessionMarkdown = (origin: string, threadId: string, title: string) =>
  `[stage session: ${label(title)}](${sessionUrl(origin, threadId)})`;

export const refPath = (ref: StageRef) => (ref.kind === "task" ? `/tasks/${ref.id}` : `/sessions/${encodeURIComponent(ref.id)}`);

/** An http(s) URL whose path ends in tasks/<n> or sessions/<id>, on any host; else null. */
export function parseStageRef(href: string): StageRef | null {
  let u: URL;
  try {
    u = new URL(href);
  } catch {
    return null;
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") return null;
  const segs = u.pathname.split("/").filter((s) => s !== "");
  if (segs.length < 2) return null;
  const kind = segs[segs.length - 2];
  const raw = segs[segs.length - 1] ?? "";
  if (kind === "tasks") {
    if (!/^[1-9]\d{0,15}$/.test(raw)) return null;
    return { kind: "task", id: Number(raw) };
  }
  if (kind === "sessions") {
    try {
      const id = decodeURIComponent(raw);
      return id === "" ? null : { kind: "session", id };
    } catch {
      return null;
    }
  }
  return null;
}

export type TextPart = { kind: "text"; text: string } | { kind: "ref"; ref: StageRef; text: string; href: string };

// A markdown link (label may hold escaped brackets) or a bare http(s) URL.
const LINK = /\[((?:\\.|[^\]\\\n])*)\]\((https?:\/\/[^\s)]+)\)|https?:\/\/[^\s<>()[\]"'`]+/g;
const TRAILING = /[.,;:!?]+$/;

/** Split plain text (a prompt, a handoff block) into text and stage-link parts; other links stay text. */
export function splitStageRefs(text: string): TextPart[] {
  const out: TextPart[] = [];
  let last = 0;
  const push = (t: string) => {
    if (t === "") return;
    const prev = out[out.length - 1];
    if (prev?.kind === "text") prev.text += t;
    else out.push({ kind: "text", text: t });
  };
  for (const m of text.matchAll(LINK)) {
    const start = m.index;
    let raw = m[0];
    const mdHref = m[2];
    let href = mdHref ?? raw;
    if (mdHref === undefined) {
      const trail = TRAILING.exec(raw)?.[0] ?? "";
      raw = raw.slice(0, raw.length - trail.length);
      href = raw;
    }
    const ref = parseStageRef(href);
    if (ref === null) continue;
    push(text.slice(last, start));
    out.push({ kind: "ref", ref, text: raw, href });
    last = start + raw.length;
  }
  push(text.slice(last));
  return out;
}
