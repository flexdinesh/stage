import { useState } from "react";
import { Button } from "./ui.tsx";

/** Clipboard API needs a secure context; over plain-http LAN fall back to a hidden textarea + execCommand. */
async function copyText(text: string): Promise<void> {
  if (window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const el = document.createElement("textarea");
  el.value = text;
  el.style.position = "fixed";
  el.style.opacity = "0";
  document.body.append(el);
  el.select();
  const ok = document.execCommand("copy");
  el.remove();
  if (!ok) throw new Error("copy failed");
}

export function CopyButton({ text, label }: { text: string; label: string }) {
  const [status, setStatus] = useState<"idle" | "copied" | "failed">("idle");
  return (
    <Button
      variant="secondary"
      small
      title={text}
      onClick={() => {
        copyText(text).then(
          () => setStatus("copied"),
          () => setStatus("failed"),
        );
      }}
      onMouseLeave={() => setStatus("idle")}
    >
      {status === "copied" ? "Copied" : status === "failed" ? "Copy failed" : label}
    </Button>
  );
}
