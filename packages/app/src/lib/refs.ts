/**
 * Stage links: copyable task, tree, schedule, project and journal-entry references and recognition of those and
 * historical session links. Agents resolve the same links with `read_ref` (server/internal/refs); the host is ignored
 * there and here, because one stage is reached at several addresses (loopback, tailnet, phone). The desktop's deep
 * links (`stage://tasks/12`, one scheme per installation identity) read the same.
 */

export type StageRef =
  | { kind: "task"; id: number }
  | { kind: "tree"; id: number }
  | { kind: "schedule"; id: number }
  | { kind: "project"; id: number }
  | { kind: "journal"; owner: "task" | "tree"; ownerId: number; entryId: number }
  | { kind: "session"; id: string; connectionId?: number };

/** Turns an app path into the copyable link (the platform's `links.stageLink`). */
export type StageLink = (path: string) => string;

export const taskUrl = (link: StageLink, id: number) => link(`/tasks/${id}`);

export const treeUrl = (link: StageLink, id: number) => link(`/trees/${id}`);

export const scheduleUrl = (link: StageLink, id: number) => link(`/schedules/${id}`);

export const projectUrl = (link: StageLink, id: number) => link(`/projects/${id}`);

/** A journal entry on its owner's page: `/tasks/12?entry=45` or `/trees/4?entry=45`. */
export const journalEntryUrl = (link: StageLink, owner: "task" | "tree", ownerId: number, entryId: number) =>
  link(`/${owner}s/${ownerId}?entry=${entryId}`);

/** Escape text for a markdown link label. */
const label = (text: string) => text.replace(/[\\[\]]/g, (c) => `\\${c}`).replace(/\s+/g, " ").trim();

/** `[stage task #12: Title](url)`: names itself, so an agent knows what it is even without stage's instructions. */
export const taskMarkdown = (link: StageLink, id: number, title: string) =>
  `[stage task #${id}: ${label(title)}](${taskUrl(link, id)})`;

/** `[stage tree #4: Title](url)`: an agent reads it with `read_ref` or edits it with the tree tools (id 4). */
export const treeMarkdown = (link: StageLink, id: number, title: string) =>
  `[stage tree #${id}: ${label(title)}](${treeUrl(link, id)})`;

/** `[stage schedule #7: Title](url)`. */
export const scheduleMarkdown = (link: StageLink, id: number, title: string) =>
  `[stage schedule #${id}: ${label(title)}](${scheduleUrl(link, id)})`;

/** `[stage project #3: Title](url)`. */
export const projectMarkdown = (link: StageLink, id: number, title: string) =>
  `[stage project #${id}: ${label(title)}](${projectUrl(link, id)})`;

/** `[stage note #45 on task #12](url)`. */
export const journalEntryMarkdown = (link: StageLink, owner: "task" | "tree", ownerId: number, entryId: number) =>
  `[stage note #${entryId} on ${owner} #${ownerId}](${journalEntryUrl(link, owner, ownerId, entryId)})`;

/** Desktop deep-link schemes: `stage:` and developer identities' `stage-<suffix>:`. */
const DEEP_LINK = /^stage(-[a-z0-9-]+)?:$/;

const ID = /^[1-9]\d{0,15}$/;

/** A numbered kind's path segment (`tasks`), or null. */
function numbered(seg: string): "task" | "tree" | "schedule" | "project" | null {
  switch (seg) {
    case "tasks":
      return "task";
    case "trees":
      return "tree";
    case "schedules":
      return "schedule";
    case "projects":
      return "project";
  }
  return null;
}

/**
 * An http(s) URL whose path ends in tasks/<n>, trees/<n> or sessions/<id>, or is exactly schedules/<n> or
 * projects/<n>, on any host, or a desktop deep link (`stage://tasks/<n>`, where the host is the kind, or `stage://localhost/tasks/<n>`); else
 * null. A task or tree link with `?entry=<n>` names that journal entry.
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
    if (segs.length !== 2 && !(segs.length === 4 && segs[0] === "connections" && segs[2] === "sessions")) return null;
  } else if (u.protocol !== "http:" && u.protocol !== "https:") return null;
  if (segs.length < 2) return null;
  const seg = segs[segs.length - 2] ?? "";
  const raw = segs[segs.length - 1] ?? "";
  const kind = numbered(seg);
  if (kind !== null) {
    // Schedule and project links are the whole path, so other sites' `/orgs/x/projects/3` stay plain.
    if (!ID.test(raw) || ((kind === "schedule" || kind === "project") && segs.length !== 2)) return null;
    const id = Number(raw);
    const entry = u.searchParams.get("entry");
    if ((kind === "task" || kind === "tree") && entry !== null && ID.test(entry))
      return { kind: "journal", owner: kind, ownerId: id, entryId: Number(entry) };
    return { kind, id };
  }
  if (seg === "sessions") {
    try {
      const id = decodeURIComponent(raw);
      if (!id) return null;
      if (segs.at(-4) === "connections") {
        const connectionId = Number(segs.at(-3));
        return Number.isSafeInteger(connectionId) && connectionId > 0 ? { kind: "session", id, connectionId } : null;
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
