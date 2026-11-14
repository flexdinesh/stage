import { Marked } from "marked";

const ESCAPES: Record<string, string> = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ESCAPES[c] ?? c);
}

// GFM markdown; raw HTML in a reply renders as literal text, never as markup.
const marked = new Marked({
  gfm: true,
  async: false,
  renderer: {
    html({ text }) {
      return escapeHtml(text);
    },
  },
});

/** Reply markdown to an HTML string. Not sanitized: pass it through DOMPurify before rendering. */
export function markdownToHtml(md: string): string {
  return marked.parse(md, { async: false });
}
