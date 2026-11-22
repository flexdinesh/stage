import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useLocation, useNavigate, useNavigationType } from "react-router";
import {
  backList,
  canGoBack,
  canGoForward,
  forwardList,
  historyIndex,
  labelCurrent,
  recordNavigation,
  startHistory,
  type HistoryJump,
} from "../lib/history.ts";
import { IconBack, IconForward } from "./icons.tsx";
import { NavLabelContext } from "./navContext.ts";
import { IconButton, Menu, MenuItem, cx, useMenu } from "./ui.tsx";

interface NavHistoryValue {
  canBack: boolean;
  canForward: boolean;
  go: (delta: number) => void;
  backList: () => HistoryJump[];
  forwardList: () => HistoryJump[];
}

const NavHistoryContext = createContext<NavHistoryValue | null>(null);

/** Follows this tab's in-app history (router index from `history.state`) for the arrows and the history list. */
export function NavHistoryProvider({ children }: { children: ReactNode }) {
  const location = useLocation();
  const type = useNavigationType();
  const navigate = useNavigate();
  const path = location.pathname + location.search;
  const [history, setHistory] = useState(() => startHistory(historyIndex(window.history.state), path));
  const [seenKey, setSeenKey] = useState(location.key);
  if (location.key !== seenKey) {
    setSeenKey(location.key);
    setHistory((h) => recordNavigation(h, historyIndex(window.history.state), type, path));
  }

  const go = useCallback((delta: number) => void navigate(delta), [navigate]);
  const label = useCallback((name: string) => setHistory((h) => labelCurrent(h, path, name)), [path]);
  const value = useMemo<NavHistoryValue>(
    () => ({
      canBack: canGoBack(history),
      canForward: canGoForward(history),
      go,
      backList: () => backList(history),
      forwardList: () => forwardList(history),
    }),
    [history, go],
  );
  return (
    <NavHistoryContext.Provider value={value}>
      <NavLabelContext.Provider value={label}>{children}</NavLabelContext.Provider>
    </NavHistoryContext.Provider>
  );
}

function useNavHistory(): NavHistoryValue {
  const value = useContext(NavHistoryContext);
  if (value === null) throw new Error("NavHistoryProvider is missing");
  return value;
}

export function useCanGoBack(): boolean {
  return useNavHistory().canBack;
}

const LONG_PRESS_MS = 500;

/**
 * One arrow: a click goes one step; right-click or a long press opens the history list that way. The list is a menu
 * anchored under the arrow; arrow keys move through it, Esc or an outside click closes it.
 */
function NavArrow({ dir, variant, className }: { dir: "back" | "forward"; variant: "ghost" | "secondary"; className?: string }) {
  const nav = useNavHistory();
  const menu = useMenu();
  const press = useRef<number | null>(null);
  const pressed = useRef(false);
  const [jumps, setJumps] = useState<HistoryJump[]>([]);
  const enabled = dir === "back" ? nav.canBack : nav.canForward;
  const label = dir === "back" ? "Back" : "Forward";

  useEffect(() => () => {
    if (press.current !== null) window.clearTimeout(press.current);
  }, []);

  const openList = () => {
    const list = dir === "back" ? nav.backList() : nav.forwardList();
    if (list.length === 0) return;
    setJumps(list);
    menu.show();
  };
  const cancelPress = () => {
    if (press.current !== null) window.clearTimeout(press.current);
    press.current = null;
  };

  return (
    <>
      <IconButton
        {...menu.anchorProps}
        variant={variant}
        icon={dir === "back" ? IconBack : IconForward}
        disabled={!enabled}
        label={label}
        title={`${label} (right-click for history)`}
        onClick={() => {
          if (pressed.current) {
            pressed.current = false;
            return;
          }
          nav.go(dir === "back" ? -1 : 1);
        }}
        onContextMenu={(e) => {
          e.preventDefault();
          cancelPress();
          openList();
        }}
        onPointerDown={(e) => {
          if (e.button !== 0) return;
          pressed.current = false;
          cancelPress();
          press.current = window.setTimeout(() => {
            press.current = null;
            pressed.current = true;
            openList();
          }, LONG_PRESS_MS);
        }}
        onPointerUp={cancelPress}
        onPointerLeave={cancelPress}
        onPointerCancel={cancelPress}
        className={cx(className, "[-webkit-touch-callout:none] select-none")}
      />
      <Menu menu={menu} label={`${label} history`} className="w-64">
        {jumps.map((j) => (
          <MenuItem key={j.delta} leading={j.kind ?? undefined} onSelect={() => nav.go(j.delta)}>
            {j.label}
          </MenuItem>
        ))}
      </Menu>
    </>
  );
}

/** Laptop content bar arrows. */
export function BackForward() {
  return (
    <div className="flex shrink-0 items-center gap-0.5">
      <NavArrow dir="back" variant="ghost" className="enabled:text-fg" />
      <NavArrow dir="forward" variant="ghost" className="enabled:text-fg" />
    </div>
  );
}

/** Phone top bar Back; long-press lists the pages behind. */
export function PhoneBack() {
  return <NavArrow dir="back" variant="secondary" />;
}
