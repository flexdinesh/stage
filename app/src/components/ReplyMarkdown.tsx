import DOMPurify from "dompurify";
import { useMemo } from "react";
import { ALLOWED_URI, markdownToHtml } from "../lib/markdown.ts";
import { useRefSlots } from "./StageLinks.tsx";

// Links in a reply open outside stage; DOMPurify drops javascript: and other unsafe URLs (ALLOWED_URI keeps
// t3code: and stage deep links, which the host routes).
DOMPurify.addHook("afterSanitizeAttributes", (node) => {
  if (node.tagName === "A" && node.hasAttribute("href")) {
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer");
  }
});

/** Sanitized markdown body of a reply (lazy-loaded with marked + DOMPurify). `refs` renders stage links as chips. */
export default function ReplyMarkdown({ text, refs = false }: { text: string; refs?: boolean }) {
  const html = useMemo(() => DOMPurify.sanitize(markdownToHtml(text), { USE_PROFILES: { html: true }, ALLOWED_URI_REGEXP: ALLOWED_URI }), [text]);
  const [root, chips] = useRefSlots(html, refs);
  return (
    <>
      <div ref={root} className="prose-reply" dangerouslySetInnerHTML={{ __html: html }} />
      {chips}
    </>
  );
}
