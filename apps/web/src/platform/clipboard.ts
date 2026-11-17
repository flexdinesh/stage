import type { Clipboard } from "@stage/app/platform";

export interface ClipboardDeps {
  secure: () => boolean;
  writeText: (text: string) => Promise<void>;
  /** Hidden-textarea copy; false when the browser refused. */
  legacyCopy: (text: string) => boolean;
}

function legacyCopy(text: string): boolean {
  const el = document.createElement("textarea");
  el.value = text;
  el.style.position = "fixed";
  el.style.opacity = "0";
  document.body.append(el);
  el.select();
  const ok = document.execCommand("copy");
  el.remove();
  return ok;
}

/** Clipboard API needs a secure context; over plain-http LAN fall back to a hidden textarea + execCommand. */
export function browserClipboard(
  deps: ClipboardDeps = {
    secure: () => window.isSecureContext,
    writeText: (text) => navigator.clipboard.writeText(text),
    legacyCopy,
  },
): Clipboard {
  return {
    async writeText(text) {
      if (deps.secure()) {
        await deps.writeText(text);
        return;
      }
      if (!deps.legacyCopy(text)) throw new Error("copy failed");
    },
  };
}
