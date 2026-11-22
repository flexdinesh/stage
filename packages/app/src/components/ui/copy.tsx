import { useEffect, useState } from "react";
import { usePlatform } from "../../platform/context.tsx";
import { Button } from "./button.tsx";

export type CopyStatus = "idle" | "copied" | "failed";

/** Copy with a status that resets after a moment. */
export function useCopy(): [CopyStatus, (text: string) => void] {
  const { clipboard } = usePlatform();
  const [status, setStatus] = useState<CopyStatus>("idle");
  useEffect(() => {
    if (status === "idle") return;
    const t = setTimeout(() => setStatus("idle"), 1500);
    return () => clearTimeout(t);
  }, [status]);
  const copy = (text: string) => {
    clipboard.writeText(text).then(
      () => setStatus("copied"),
      () => setStatus("failed"),
    );
  };
  return [status, copy];
}

/** A copy control's label for its status: "Copied", "Copy failed", else the idle label. */
export function copyStatusText(status: CopyStatus, idle: string): string {
  return status === "copied" ? "Copied" : status === "failed" ? "Copy failed" : idle;
}

/** Copy button whose label confirms the copy for 1.5s; the result is announced politely. */
export function CopyButton({ text, label, small = true, title }: { text: string; label: string; small?: boolean; title?: string }) {
  const [status, copy] = useCopy();
  return (
    <>
      <Button small={small} title={title} onClick={() => copy(text)}>
        {copyStatusText(status, label)}
      </Button>
      <span className="sr-only" aria-live="polite">
        {status === "copied" ? `${label}: copied` : status === "failed" ? `${label}: copy failed` : ""}
      </span>
    </>
  );
}

/** A remedy command in a Bench Gray mono box with Copy. */
export function CommandBox({ command }: { command: string }) {
  return (
    <div className="flex items-start gap-2 border border-line bg-panel px-3 py-2">
      <code className="min-w-0 flex-1 font-mono text-detail break-all text-fg">{command}</code>
      <CopyButton text={command} label="Copy" />
    </div>
  );
}
