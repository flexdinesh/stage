import { useState } from "react";
import { usePlatform } from "../platform/context.tsx";
import { Button } from "./ui.tsx";

export function CopyButton({ text, label }: { text: string; label: string }) {
  const { clipboard } = usePlatform();
  const [status, setStatus] = useState<"idle" | "copied" | "failed">("idle");
  return (
    <Button
      variant="secondary"
      small
      title={text}
      onClick={() => {
        clipboard.writeText(text).then(
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
