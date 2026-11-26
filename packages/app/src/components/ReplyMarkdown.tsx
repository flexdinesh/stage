import DOMPurify from "dompurify";
import { useMemo } from "react";
import { useHarnesses } from "../api/queries.ts";
import { linkSchemes } from "../lib/harnesses.ts";
import { allowedUri, markdownToHtml } from "../lib/markdown.ts";
import { useRefSlots } from "./StageLinks.tsx";

// Reply links open outside Stage. DOMPurify applies the allowed schemes before the host routes a link.
DOMPurify.addHook("afterSanitizeAttributes", (node) => {
  if (node.tagName === "A" && node.hasAttribute("href")) {
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer");
  }
});

/** Sanitized markdown body of a reply (lazy-loaded with marked + DOMPurify). `refs` renders stage links as chips. */
export default function ReplyMarkdown({ text, refs = false }: { text: string; refs?: boolean }) {
  const { descriptors } = useHarnesses();
  const schemes = useMemo(() => linkSchemes(descriptors), [descriptors]);
  const schemeKey = schemes.join(",");
  const html = useMemo(() => DOMPurify.sanitize(markdownToHtml(text), {
    USE_PROFILES: { html: true },
    ALLOWED_URI_REGEXP: allowedUri(schemeKey === "" ? [] : schemeKey.split(",")),
  }), [text, schemeKey]);
  const [root, chips] = useRefSlots(html, refs);
  return (
    <>
      <div ref={root} className="prose-reply" dangerouslySetInnerHTML={{ __html: html }} />
      {chips}
    </>
  );
}
