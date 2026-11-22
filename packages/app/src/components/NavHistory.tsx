import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
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
import { buttonClass, cx } from "./ui.tsx";

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

/** Static anchor names (Tailwind needs literal classes); one per arrow on screen. */
const ANCHORS = {
  back: { anchor: "[anchor-name:--nav-back]", menu: "[position-anchor:--nav-back]" },
  forward: { anchor: "[anchor-name:--nav-forward]", menu: "[position-anchor:--nav-forward]" },
  phoneBack: { anchor: "[anchor-name:--nav-back-phone]", menu: "[position-anchor:--nav-back-phone]" },
} as const;

/**
 * One arrow: a click goes one step; right-click or a long press opens the history list that way. The list is a popover
 * anchored under the arrow; arrow keys move through it, Esc or an outside click closes it.
 */
function NavArrow({
  dir,
  anchor,
  className,
  iconSize,
}: {
  dir: "back" | "forward";
  anchor: keyof typeof ANCHORS;
  className: string;
  iconSize: number;
}) {
  const nav = useNavHistory();
  const menuId = useId();
  const menu = useRef<HTMLDivElement>(null);
  const button = useRef<HTMLButtonElement>(null);
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
    menu.current?.showPopover();
    requestAnimationFrame(() => menu.current?.querySelector<HTMLButtonElement>("button")?.focus());
  };
  const cancelPress = () => {
    if (press.current !== null) window.clearTimeout(press.current);
    press.current = null;
  };
  const pick = (delta: number) => {
    menu.current?.hidePopover();
    nav.go(delta);
  };
  const onMenuKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const items = [...(menu.current?.querySelectorAll<HTMLButtonElement>("button") ?? [])];
    const at = items.findIndex((b) => b === document.activeElement);
    const next = e.key === "ArrowDown" ? (at + 1) % items.length : (at - 1 + items.length) % items.length;
    items[next]?.focus();
  };

  return (
    <>
      <button
        ref={button}
        type="button"
        disabled={!enabled}
        aria-label={label}
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
        className={cx(className, ANCHORS[anchor].anchor, "[-webkit-touch-callout:none] select-none")}
      >
        {dir === "back" ? <IconBack size={iconSize} /> : <IconForward size={iconSize} />}
      </button>
      <div
        ref={menu}
        id={menuId}
        popover="auto"
        aria-label={`${label} history`}
        onKeyDown={onMenuKey}
        onToggle={(e) => {
          if (e.newState === "closed" && menu.current?.contains(document.activeElement) === true) button.current?.focus();
        }}
        className={cx(
          "m-0 mt-1 w-64 border border-line-strong bg-surface text-fg [top:anchor(bottom)] [left:anchor(left)]",
          ANCHORS[anchor].menu,
        )}
      >
        <div className="flex flex-col py-1">
          {jumps.map((j) => (
            <button
              key={j.delta}
              type="button"
              onClick={() => pick(j.delta)}
              className="flex min-h-11 min-w-0 items-center gap-2 px-3 text-left text-ui outline-none hover:bg-panel focus-visible:bg-panel focus-visible:shadow-[inset_0_0_0_1px_var(--line-strong)] md:min-h-8"
            >
              {j.kind === null ? null : <span className="shrink-0 font-mono text-meta-sm text-muted">{j.kind}</span>}
              <span className="min-w-0 truncate">{j.label}</span>
            </button>
          ))}
        </div>
      </div>
    </>
  );
}

const laptopArrow = cx(buttonClass("ghost"), "w-8 px-0 enabled:text-fg");

/** Laptop content bar arrows. */
export function BackForward() {
  return (
    <div className="flex shrink-0 items-center gap-0.5">
      <NavArrow dir="back" anchor="back" iconSize={16} className={laptopArrow} />
      <NavArrow dir="forward" anchor="forward" iconSize={16} className={laptopArrow} />
    </div>
  );
}

/** Phone top bar Back; long-press lists the pages behind. */
export function PhoneBack() {
  return <NavArrow dir="back" anchor="phoneBack" iconSize={18} className={cx(buttonClass("secondary"), "w-10 px-0")} />;
}
