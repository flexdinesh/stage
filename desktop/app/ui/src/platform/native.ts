import type { HostState } from "@stage/app/platform";

/**
 * The Go side of the desktop app as the adapters use it: the generated MyGo client (src/mygo.ts) wired in
 * main.tsx, or a fake in tests and the dev preview.
 */
export interface DesktopNative {
  state(): Promise<HostState>;
  /** Every state change. Returns an unsubscribe. */
  onState(listener: (state: HostState) => void): () => void;
  /** App paths the shell asks to show (deep links, a second launch). Returns an unsubscribe. */
  onNavigate(listener: (path: string) => void): () => void;
  /** The deep link the app was launched with, once; "" when none. */
  takeNavigation(): Promise<string>;
  retry(): Promise<void>;
  startServer(): Promise<void>;
  restartServer(): Promise<void>;
  diagnostics(): Promise<string>;
  copyText(text: string): Promise<void>;
  openExternal(url: string): Promise<void>;
  openApp(url: string): Promise<void>;
  loginStartup(): Promise<boolean>;
  setLoginStartup(enabled: boolean): Promise<void>;
}

/** Before the shell's first answer: starting, nothing known yet. */
export const INITIAL_STATE: HostState = {
  phase: "starting",
  step: null,
  title: "Starting Stage",
  message: "",
  fix: "",
  command: "",
  problems: [],
  logTail: [],
  app: { name: "Stage", version: "", identity: "", apiVersion: 0 },
  server: null,
  foreground: false,
  versionSkew: false,
  epoch: 0,
  busy: false,
  dev: false,
};
