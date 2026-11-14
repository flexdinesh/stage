import DOMPurify from "dompurify";
import { useMemo } from "react";
import { markdownToHtml } from "../lib/markdown.ts";

// Links in a reply open outside stage; DOMPurify already drops javascript: and other unsafe URLs.
DOMPurify.addHook("afterSanitizeAttributes", (node) => {
  if (node.tagName === "A" && node.hasAttribute("href")) {
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer");
  }
});

/** Sanitized markdown body of a reply (lazy-loaded with marked + DOMPurify). */
export default function ReplyMarkdown({ text }: { text: string }) {
  const html = useMemo(() => DOMPurify.sanitize(markdownToHtml(text), { USE_PROFILES: { html: true } }), [text]);
  return <div className="prose-reply" dangerouslySetInnerHTML={{ __html: html }} />;
}
