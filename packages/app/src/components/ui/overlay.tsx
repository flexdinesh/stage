import {
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { IconClose } from "../icons.tsx";
import { IconButton } from "./button.tsx";
import { cx } from "./cx.ts";
import { Kbd } from "./tone.tsx";

const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';

/** A floating surface's box (menus, popovers, listboxes): flat Paper with a Strong Hairline, never a shadow. */
export const surfaceClass = "border border-line-strong bg-surface text-fg";

// ---- Focus and dismissal ----

/**
 * Closes a non-modal surface on Esc (focus returns to the `returnFocusId` element), on a pointer press outside the
 * `insideId` element, and when focus leaves it by keyboard. `insideId` names the wrapper holding trigger and surface.
 */
export function useDismiss({
  open,
  onClose,
  insideId,
  returnFocusId,
}: {
  open: boolean;
  onClose: () => void;
  insideId: string;
  returnFocusId?: string;
}) {
  const onCloseRef = useRef(onClose);
  useEffect(() => {
    onCloseRef.current = onClose;
  });
  useEffect(() => {
    if (!open) return;
    const wrap = document.getElementById(insideId);
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape") return;
      e.stopPropagation();
      onCloseRef.current();
      if (returnFocusId !== undefined) document.getElementById(returnFocusId)?.focus();
    }
    function onPointer(e: PointerEvent) {
      if (e.target instanceof Node && wrap?.contains(e.target) === true) return;
      onCloseRef.current();
    }
    // A click leaves no related target (WebKit does not focus clicked buttons), so outside clicks are the pointer
    // handler's; this one catches Tab moving focus out.
    function onFocusOut(e: FocusEvent) {
      if (e.relatedTarget instanceof Node && wrap?.contains(e.relatedTarget) !== true) onCloseRef.current();
    }
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointer);
    wrap?.addEventListener("focusout", onFocusOut);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointer);
      wrap?.removeEventListener("focusout", onFocusOut);
    };
  }, [open, insideId, returnFocusId]);
}

/** Next index for a list key (wraps at both ends); null when the key is not a list key. */
export function rovingIndex(key: string, at: number, count: number): number | null {
  if (count === 0) return null;
  if (key === "ArrowDown") return at < 0 ? 0 : (at + 1) % count;
  if (key === "ArrowUp") return at < 0 ? count - 1 : (at - 1 + count) % count;
  if (key === "Home") return 0;
  if (key === "End") return count - 1;
  return null;
}

/**
 * Keydown handler for a list container: arrow keys (wrapping), Home and End move DOM focus among its enabled
 * `selector` items.
 */
export function rovingKeyDown(selector: string) {
  return (e: ReactKeyboardEvent<HTMLElement>) => {
    const items = [...e.currentTarget.querySelectorAll<HTMLElement>(selector)].filter((el) => !(el instanceof HTMLButtonElement && el.disabled));
    const at = items.findIndex((el) => el === document.activeElement);
    const next = rovingIndex(e.key, at, items.length);
    if (next === null) return;
    e.preventDefault();
    items[next]?.focus();
  };
}

/**
 * Modal behavior for a dialog element: initial focus (`[data-autofocus]`, else the first focusable), Tab trapped
 * inside, Esc closes, body scroll locked, focus restored to where it was on close.
 */
export function useModal(panel: RefObject<HTMLElement | null>, onClose: () => void) {
  const onCloseRef = useRef(onClose);
  useEffect(() => {
    onCloseRef.current = onClose;
  });

  useEffect(() => {
    const previous = document.activeElement;
    const el = panel.current;
    const first = el?.querySelector<HTMLElement>("[data-autofocus]") ?? el?.querySelector<HTMLElement>(FOCUSABLE);
    first?.focus();
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";

    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.stopPropagation();
        onCloseRef.current();
        return;
      }
      if (e.key !== "Tab" || el === null) return;
      const items = [...el.querySelectorAll<HTMLElement>(FOCUSABLE)];
      const head = items[0];
      const tail = items[items.length - 1];
      if (head === undefined || tail === undefined) return;
      if (e.shiftKey && document.activeElement === head) {
        e.preventDefault();
        tail.focus();
      } else if (!e.shiftKey && document.activeElement === tail) {
        e.preventDefault();
        head.focus();
      }
    }
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = overflow;
      if (previous instanceof HTMLElement) previous.focus();
    };
  }, [panel]);
}

// ---- Modal layer ----

/** A modal dialog over a scrim (portal to body). Style and place the panel with `className`. */
export function ModalLayer({
  label,
  onClose,
  className,
  children,
}: {
  label: string;
  onClose: () => void;
  className?: string;
  children: ReactNode;
}) {
  const panel = useRef<HTMLDivElement>(null);
  useModal(panel, onClose);
  return createPortal(
    <div className="fixed inset-0 z-50">
      <div className="absolute inset-0 bg-scrim" onClick={onClose} />
      <div ref={panel} role="dialog" aria-modal="true" aria-label={label} className={cx("absolute", className)}>
        {children}
      </div>
    </div>,
    document.body,
  );
}

/** Side drawer (laptop) / full-screen sheet (phone). Esc closes, focus is trapped and restored. */
export function Drawer({
  title,
  onClose,
  wide = false,
  footer,
  children,
}: {
  title: string;
  onClose: () => void;
  wide?: boolean;
  footer?: ReactNode;
  children: ReactNode;
}) {
  return (
    <ModalLayer
      label={title}
      onClose={onClose}
      className={cx(
        "inset-0 flex flex-col bg-bg md:inset-y-0 md:right-0 md:left-auto md:w-full md:border-l md:border-line-strong",
        wide ? "md:max-w-[680px]" : "md:max-w-[580px]",
      )}
    >
      <div className="flex h-[calc(3.25rem+var(--inset-top))] shrink-0 items-center gap-3 border-b border-line pt-[var(--inset-top)] pr-[max(1rem,env(safe-area-inset-right))] pl-[max(1rem,env(safe-area-inset-left))] md:h-12 md:pt-0 md:pr-4 md:pl-4">
        <h2 className="text-title font-semibold">{title}</h2>
        <div className="ml-auto flex items-center gap-3">
          <Kbd className="max-md:hidden">Esc</Kbd>
          <IconButton label="Close" icon={IconClose} small onClick={onClose} title="" />
        </div>
      </div>
      <div
        className={cx(
          "min-h-0 flex-1 overflow-y-auto p-4 max-md:pr-[max(1rem,env(safe-area-inset-right))] max-md:pl-[max(1rem,env(safe-area-inset-left))]",
          footer === undefined && "max-md:pb-[calc(1rem+env(safe-area-inset-bottom))]",
        )}
      >
        {children}
      </div>
      {footer === undefined ? null : (
        <div className="flex shrink-0 flex-wrap items-center gap-2 border-t border-line bg-panel px-4 py-3 max-md:pr-[max(1rem,env(safe-area-inset-right))] max-md:pb-[calc(0.75rem+env(safe-area-inset-bottom))] max-md:pl-[max(1rem,env(safe-area-inset-left))]">
          {footer}
        </div>
      )}
    </ModalLayer>
  );
}

// ---- Popover (non-modal panel) ----

export interface PopoverState {
  open: boolean;
  setOpen: (open: boolean) => void;
  close: () => void;
  panelId: string;
  /** Spread on the element holding trigger and panel (positioning context). */
  wrapProps: { id: string };
  /** Spread on the trigger button. */
  triggerProps: { id: string; "aria-expanded": boolean; "aria-controls": string; onClick: () => void };
}

/**
 * State for a panel that opens from a trigger (server picker, status details): Esc returns focus to the trigger;
 * an outside press or Tab away closes it. Spread `wrapProps` on the element holding trigger and panel and
 * `triggerProps` on the trigger; render the panel with `PopoverPanel`.
 */
export function usePopover(onClose?: () => void): PopoverState {
  const [open, setOpen] = useState(false);
  const base = useId();
  const wrapId = `${base}wrap`;
  const triggerId = `${base}trigger`;
  const panelId = `${base}panel`;
  const close = useCallback(() => {
    setOpen(false);
    onClose?.();
  }, [onClose]);
  useDismiss({ open, onClose: close, insideId: wrapId, returnFocusId: triggerId });
  return {
    open,
    setOpen,
    close,
    panelId,
    wrapProps: { id: wrapId },
    triggerProps: { id: triggerId, "aria-expanded": open, "aria-controls": panelId, onClick: () => setOpen(!open) },
  };
}

/** The panel of a `usePopover`: rendered only while open. Position it with `className`. */
export function PopoverPanel({
  popover,
  label,
  className,
  children,
}: {
  popover: PopoverState;
  label?: string;
  className?: string;
  children: ReactNode;
}) {
  if (!popover.open) return null;
  return (
    <div id={popover.panelId} aria-label={label} className={cx("z-50", surfaceClass, className)}>
      {children}
    </div>
  );
}

// ---- Menu (native popover, anchored) ----

export interface MenuState {
  id: string;
  triggerId: string;
  anchor: string;
  /** Opens the menu (for triggers that open it themselves, e.g. a long press). */
  show: () => void;
  hide: () => void;
  /** Spread on a trigger that toggles the menu on click. */
  triggerProps: { id: string; popoverTarget: string; style: { anchorName: string } };
  /** Spread on a trigger that opens the menu itself (`show`). */
  anchorProps: { id: string; style: { anchorName: string } };
}

export function useMenu(): MenuState {
  const base = useId();
  const id = `${base}menu`;
  const triggerId = `${base}trigger`;
  const anchor = `--menu-${base.replace(/[^a-zA-Z0-9_-]/g, "")}`;
  const style = { anchorName: anchor };
  return {
    id,
    triggerId,
    anchor,
    show: () => document.getElementById(id)?.showPopover(),
    hide: () => document.getElementById(id)?.hidePopover(),
    triggerProps: { id: triggerId, popoverTarget: id, style },
    anchorProps: { id: triggerId, style },
  };
}

// Each side flips when it would overflow the viewport (a right-aligned menu near the left edge on a phone).
const MENU_ALIGN = {
  left: "[top:anchor(bottom)] [left:anchor(left)] mt-1 [position-try-fallbacks:flip-inline]",
  right: "[top:anchor(bottom)] [right:anchor(right)] [left:auto] mt-1 [position-try-fallbacks:flip-inline]",
  above: "[bottom:anchor(top)] [left:anchor(left)] [top:auto] mb-1 [position-try-fallbacks:flip-inline]",
} as const;

/**
 * A menu of actions in the top layer, anchored under its trigger (light-dismiss, Esc). Opening focuses the first
 * item; arrows (wrapping), Home and End move; closing returns focus to the trigger. Children are `MenuItem`s.
 */
export function Menu({
  menu,
  label,
  align = "left",
  className,
  children,
}: {
  menu: MenuState;
  label: string;
  align?: keyof typeof MENU_ALIGN;
  /** Width, e.g. `w-52`. */
  className?: string;
  children: ReactNode;
}) {
  return (
    <div
      id={menu.id}
      popover="auto"
      role="menu"
      aria-label={label}
      style={{ positionAnchor: menu.anchor }}
      onKeyDown={rovingKeyDown('[role="menuitem"]')}
      onToggle={(e) => {
        const el = e.currentTarget;
        if (e.newState === "open") {
          requestAnimationFrame(() => el.querySelector<HTMLElement>('[role="menuitem"]:not(:disabled)')?.focus());
        } else if (el.contains(document.activeElement)) {
          document.getElementById(menu.triggerId)?.focus();
        }
      }}
      className={cx("m-0 py-1", surfaceClass, MENU_ALIGN[align], className)}
    >
      {children}
    </div>
  );
}

/** One menu action. `reason` disables it and says why; `danger` sets it in Fault Red. */
export function MenuItem({
  onSelect,
  danger = false,
  reason,
  leading,
  children,
}: {
  onSelect: () => void;
  danger?: boolean;
  reason?: string | null;
  /** Mono Slate text before the label (a history entry's kind). */
  leading?: ReactNode;
  children: ReactNode;
}) {
  const disabled = reason !== undefined && reason !== null;
  return (
    <button
      type="button"
      role="menuitem"
      disabled={disabled}
      onClick={(e) => {
        e.currentTarget.closest<HTMLElement>("[popover]")?.hidePopover();
        onSelect();
      }}
      className={cx(
        "flex min-h-11 w-full min-w-0 flex-col justify-center px-3 py-1 text-left text-ui outline-none hover:bg-panel focus-visible:bg-panel focus-visible:shadow-[inset_0_0_0_1px_var(--line-strong)] disabled:cursor-not-allowed disabled:hover:bg-transparent md:min-h-control",
        danger && "text-bad",
      )}
    >
      <span className={cx("flex min-w-0 items-center gap-2", disabled && "opacity-45")}>
        {leading === undefined ? null : <span className="shrink-0 font-mono text-meta-sm text-muted">{leading}</span>}
        <span className="min-w-0 truncate">{children}</span>
      </span>
      {disabled ? <span className="font-mono text-meta-sm text-muted">{reason}</span> : null}
    </button>
  );
}

export function MenuSeparator() {
  return <div role="separator" className="my-1 h-px bg-line-strong" />;
}

/** Renders a floating layer (a caret-anchored picker) into the document body, above the page. */
export function FloatingPortal({ children }: { children: ReactNode }) {
  return createPortal(children, document.body);
}

// ---- Shared list chrome ----

/** Keycap hints under a list (↑↓ move, ↵ select, Esc close); laptop only. */
export function KeyHints({ hints, className, children }: { hints: readonly (readonly [string, string])[]; className?: string; children?: ReactNode }) {
  return (
    <div className={cx("flex shrink-0 items-center gap-3 border-t border-line bg-panel px-3 py-1.5 font-mono text-meta-sm whitespace-nowrap text-muted max-md:hidden", className)}>
      {hints.map(([key, what]) => (
        <span key={key} className="inline-flex items-center gap-1.5">
          <Kbd>{key}</Kbd>
          {what}
        </span>
      ))}
      {children === undefined ? null : <span className="ml-auto min-w-0 truncate">{children}</span>}
    </div>
  );
}

/** Text with its matched range bold in Ink (search results, filtered options). */
export function Highlight({ text, range }: { text: string; range: { start: number; end: number } | null | undefined }) {
  if (range === null || range === undefined || range.end <= range.start) return <>{text}</>;
  return (
    <>
      {text.slice(0, range.start)}
      <b className="font-semibold text-fg">{text.slice(range.start, range.end)}</b>
      {text.slice(range.end)}
    </>
  );
}
