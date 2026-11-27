import type { GrantKind } from "../api/types.ts";

export const LOGIN_PATH = "/auth/login";

export function grantKind(pathname: string): GrantKind | null {
  switch (pathname.toLowerCase().replace(/\/+$/, "")) {
    case "/auth/setup": return "setup";
    case "/auth/invite": return "invite";
    case "/auth/reset": return "reset";
    default: return null;
  }
}

/** A return path comes from the current router location. Refuse origin and auth-route escapes. */
export function returnToWork({ pathname, search, hash }: { pathname: string; search: string; hash: string }): string {
  const path = pathname + search + hash;
  if (!pathname.startsWith("/") || path.includes("\\") || [...path].some((c) => c < " " || c.charCodeAt(0) === 127)) return "/";
  try {
    const base = new URL("https://stage.invalid");
    const resolved = new URL(path, base);
    if (resolved.origin !== base.origin || resolved.pathname.startsWith("//") || /^\/auth(?:\/|$)/i.test(resolved.pathname)) return "/";
    return resolved.pathname + resolved.search + resolved.hash;
  } catch {
    return "/";
  }
}
