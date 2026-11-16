import type { Clipboard } from "@stage/app/platform";
import type { DesktopNative } from "./native.ts";

/** The system clipboard through the shell (works without a secure-context Clipboard API). */
export function desktopClipboard(native: DesktopNative): Clipboard {
  return { writeText: (text) => native.copyText(text) };
}
