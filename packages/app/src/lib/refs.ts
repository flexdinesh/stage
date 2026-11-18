/**
 * Stage links: copyable task references and recognition of task and historical session links. Agents resolve the same
 * links with `read_ref` (server/internal/refs); the host is ignored there and here, because one stage is reached
 * at several addresses (loopback, tailnet, phone). The desktop's deep links (`stage://tasks/12`, one scheme per
 * installation identity) read the same.
 */

export type StageRef = { kind: "task"; id: number } | { kind: "session"; id: string; connectionId?: number };

/** Turns an app path into the copyable link (the platform's `links.stageLink`). */
export type StageLink = (path: string) => string;

export const taskUrl = (link: StageLink, id: number) => link(`/tasks/${id}`);

/** Escape text for a markdown link label. */
const label = (text: string) => text.replace(/[\\[\]]/g, (c) => `\\${c}`).replace(/\s+/g, " ").trim();

/** `[stage task #12: Title](url)`: names itself, so an agent knows what it is even without stage's instructions. */
export const taskMarkdown = (link: StageLink, id: number, title: string) =>
  `[stage task #${id}: ${label(title)}](${taskUrl(link, id)})`;

/** Desktop deep-link schemes: `stage:` and developer identities' `stage-<suffix>:`. */
const DEEP_LINK = /^stage(-[a-z0-9-]+)?:$/;

/**
 * An http(s) URL whose path ends in tasks/<n> or sessions/<id>, on any host, or a desktop deep link
 * (`stage://tasks/<n>`, where the host is the kind, or `stage://localhost/tasks/<n>`); else null.
 */
export function parseStageRef(href: string): StageRef | null {
  let u: URL;
  try {
    u = new URL(href);
  } catch {
    return null;
  }
  let segs = u.pathname.split("/").filter((s) => s !== "");
  if (DEEP_LINK.test(u.protocol)) {
    const host = u.hostname.toLowerCase();
    if (host !== "" && host !== "localhost") segs = [host, ...segs];
    if (segs.length !== 2 && !(segs.length===4 && segs[0]==="connections" && segs[2]==="sessions")) return null;
  } else if (u.protocol !== "http:" && u.protocol !== "https:") return null;
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
      if (!id) return null;
      if(segs.at(-4)==="connections") {
        const connectionId=Number(segs.at(-3));
        return Number.isSafeInteger(connectionId) && connectionId>0 ? {kind:"session",id,connectionId}:null;
      }
      return { kind: "session", id };
    } catch {
      return null;
    }
  }
  return null;
}

export type TextPart = { kind: "text"; text: string } | { kind: "ref"; ref: StageRef; text: string; href: string };

// A markdown link (label may hold escaped brackets) or a bare http(s) or stage deep-link URL.
const LINK =
  /\[((?:\\.|[^\]\\\n])*)\]\(((?:https?|stage(?:-[a-z0-9-]+)?):\/\/[^\s)]+)\)|(?:https?|stage(?:-[a-z0-9-]+)?):\/\/[^\s<>()[\]"'`]+/gi;
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
