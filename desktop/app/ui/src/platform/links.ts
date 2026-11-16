import type { HostState, Links } from "@stage/app/platform";
import type { DesktopNative } from "./native.ts";

/**
 * Copied Stage links: the server's public URL (STAGE_PUBLIC_URL) when it has one, so the link opens anywhere;
 * otherwise the installation's deep link (`stage://tasks/12`), never the page's private mygo:// origin.
 * Opening goes through the shell, which checks the URL and hands it to the OS.
 */
export function desktopLinks(native: DesktopNative, state: () => HostState): Links {
  return {
    stageLink(path) {
      const s = state();
      const publicUrl = s.server?.publicUrl ?? null;
      if (publicUrl !== null && publicUrl !== "") return publicUrl.replace(/\/+$/, "") + path;
      // The app registers its own identity's scheme, so the link reopens this installation.
      const scheme = s.app.identity !== "" ? s.app.identity : (s.server?.identity ?? "stage");
      return `${scheme}:/${path}`;
    },
    openExternal: (url) => void native.openExternal(url).catch(logOpenError(url)),
    openApp: (url) => void native.openApp(url).catch(logOpenError(url)),
  };
}

const logOpenError = (url: string) => (err: unknown) => console.error(`open ${url}:`, err);
