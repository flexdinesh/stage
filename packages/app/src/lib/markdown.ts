import { Marked } from "marked";

const ESCAPES: Record<string, string> = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ESCAPES[c] ?? c);
}

/** Link schemes kept by the sanitizer besides the web ones: T3 app links and the desktop's stage deep links. */
export const ALLOWED_URI =
  /^(?:(?:(?:f|ht)tps?|mailto|tel|t3code|stage(?:-[a-z0-9-]+)?):|[^a-z]|[a-z+.-]+(?:[^a-z+.\-:]|$))/i;

// A bare stage deep link (stage://tasks/12, stage-dev://sessions/x); GFM autolinks only http(s), www and email.
const DEEP_LINK = /^stage(?:-[a-z0-9-]+)?:\/\/[^\s<>()[\]"'`]+/i;
const TRAILING = /[.,;:!?]+$/;

// GFM markdown; raw HTML in a reply renders as literal text, never as markup.
// Images render as links: stage never loads remote images from agent output.
const marked = new Marked({
  gfm: true,
  async: false,
  extensions: [
    {
      name: "stageLink",
      level: "inline",
      start(src: string) {
        const i = src.search(/stage(?:-[a-z0-9-]+)?:\/\//i);
        return i < 0 ? undefined : i;
      },
      tokenizer(src: string) {
        const m = DEEP_LINK.exec(src);
        if (m === null) return undefined;
        const raw = m[0].replace(TRAILING, "");
        return { type: "stageLink", raw, href: raw };
      },
      renderer(token) {
        const href = typeof token["href"] === "string" ? token["href"] : "";
        return `<a href="${escapeHtml(href)}">${escapeHtml(href)}</a>`;
      },
    },
  ],
  renderer: {
    html({ text }) {
      return escapeHtml(text);
    },
    image({ href, text }) {
      const label = text === "" ? href : `image: ${text}`;
      return `<a href="${escapeHtml(href)}">${escapeHtml(label)}</a>`;
    },
  },
});

/** Reply markdown to an HTML string. Not sanitized: pass it through DOMPurify before rendering. */
export function markdownToHtml(md: string): string {
  return marked.parse(md, { async: false });
}
