import { useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from "react";
import { createPortal } from "react-dom";
import type { MatchRange } from "../lib/projects.ts";
import { Chip, Kbd, cx, inputClass } from "./ui.tsx";

export interface ComboboxOption {
  value: string;
  label: string;
  /** Span of `label` to bold. */
  labelMatch?: MatchRange | null | undefined;
  /** Secondary mono line (e.g. a path). */
  detail?: string | undefined;
  /** Span of `detail` to bold. */
  detailMatch?: MatchRange | null | undefined;
  /** Plain chip after the label (e.g. "archived"). */
  chip?: string | undefined;
}

const GAP = 4;
const EDGE = 8;
const MIN_WIDTH = 280;
const MAX_HEIGHT = 344;
/** Below this much room under the input, flip above when there is more room there. */
const FLIP_BELOW = 200;

/** Fixed box under the input (above it near the bottom of the visible area, e.g. over a phone keyboard). */
function placement(input: HTMLElement): CSSProperties {
  const rect = input.getBoundingClientRect();
  const vv = window.visualViewport;
  const viewTop = vv?.offsetTop ?? 0;
  const viewBottom = viewTop + (vv?.height ?? window.innerHeight);
  const viewWidth = document.documentElement.clientWidth;
  const width = Math.min(Math.max(rect.width, MIN_WIDTH), viewWidth - 2 * EDGE);
  // Left-aligned with the input; right-aligned when wider than the room to its right.
  const left = rect.left + width <= viewWidth - EDGE ? rect.left : Math.max(EDGE, Math.min(rect.right, viewWidth - EDGE) - width);
  const spaceBelow = viewBottom - rect.bottom - GAP - EDGE;
  const spaceAbove = rect.top - GAP - EDGE - viewTop;
  if (spaceBelow < FLIP_BELOW && spaceAbove > spaceBelow) {
    const bottom = document.documentElement.clientHeight - rect.top + GAP;
    return { left, width, bottom, maxHeight: Math.min(MAX_HEIGHT, spaceAbove) };
  }
  return { left, width, top: rect.bottom + GAP, maxHeight: Math.min(MAX_HEIGHT, spaceBelow) };
}

function Mark({ text, range }: { text: string; range: MatchRange | null | undefined }) {
  if (range === null || range === undefined) return <>{text}</>;
  return (
    <>
      {text.slice(0, range.start)}
      <b className="font-semibold text-fg">{text.slice(range.start, range.end)}</b>
      {text.slice(range.end)}
    </>
  );
}

/**
 * Type-to-filter select (ARIA 1.2 editable combobox). Focus or click opens the list with the text selected, typing
 * filters, ↑↓ move, Enter commits, Esc/Tab/blur restore the selection. Only listed values can be chosen.
 */
export function Combobox({
  id,
  value,
  onChange,
  options,
  pinned = [],
  placeholder,
  emptyText,
  listLabel,
  disabled = false,
  className,
  size,
  autoFocus = false,
  "aria-label": ariaLabel,
}: {
  id?: string | undefined;
  value: string;
  onChange: (value: string) => void;
  /** Options matching `query`, ranked; `""` lists all. */
  options: (query: string) => ComboboxOption[];
  /** Listed first while the query is empty (e.g. "All projects"). */
  pinned?: readonly ComboboxOption[] | undefined;
  placeholder?: string | undefined;
  emptyText: string;
  listLabel: string;
  disabled?: boolean | undefined;
  className?: string | undefined;
  /** Intrinsic width in characters (where the layout does not stretch the input). */
  size?: number | undefined;
  /** Marks the input `data-autofocus` so an enclosing Drawer focuses it first. */
  autoFocus?: boolean | undefined;
  "aria-label"?: string | undefined;
}) {
  const listId = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  /** Set on a pointer press that focuses the input, so its mouseup does not clear the text selection. */
  const focusingClick = useRef(false);
  const [open, setOpen] = useState(false);
  /** Typed text; null shows the selected option's label. */
  const [query, setQuery] = useState<string | null>(null);
  const [active, setActive] = useState(0);
  const [style, setStyle] = useState<CSSProperties | null>(null);

  const all = useMemo(() => [...pinned, ...options("")], [pinned, options]);
  const selected = all.find((o) => o.value === value);
  const typed = query ?? "";
  const items = useMemo(() => (typed.trim() === "" ? all : options(typed)), [typed, all, options]);
  const index = Math.min(active, items.length - 1);
  const current = items[index];
  const optionId = (i: number) => `${listId}-${i}`;

  const openList = () => {
    setOpen(true);
    setActive(Math.max(0, all.findIndex((o) => o.value === value)));
  };
  const close = () => {
    setOpen(false);
    setQuery(null);
  };
  const commit = (o: ComboboxOption) => {
    if (o.value !== value) onChange(o.value);
    close();
  };

  useLayoutEffect(() => {
    const input = inputRef.current;
    if (!open || input === null) {
      setStyle(null);
      return;
    }
    const update = () => setStyle(placement(input));
    // Scrolling the list itself must not re-place it.
    const onScroll = (e: Event) => {
      if (e.target instanceof Node && panelRef.current?.contains(e.target)) return;
      update();
    };
    update();
    const vv = window.visualViewport;
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", update);
    vv?.addEventListener("resize", update);
    vv?.addEventListener("scroll", update);
    return () => {
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", update);
      vv?.removeEventListener("resize", update);
      vv?.removeEventListener("scroll", update);
    };
  }, [open]);

  // Keep the active row in view when it changes, without touching the list's scroll otherwise.
  const placed = style !== null;
  useLayoutEffect(() => {
    const box = listRef.current;
    const row = box?.children[index];
    if (!placed || box === null || !(row instanceof HTMLElement)) return;
    if (row.offsetTop < box.scrollTop) box.scrollTop = row.offsetTop;
    else if (row.offsetTop + row.offsetHeight > box.scrollTop + box.clientHeight) {
      box.scrollTop = row.offsetTop + row.offsetHeight - box.clientHeight;
    }
  }, [index, placed]);

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.ctrlKey || e.metaKey) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!open) openList();
      else if (items.length > 0) setActive((index + (e.key === "ArrowDown" ? 1 : -1) + items.length) % items.length);
      return;
    }
    if (e.altKey) return;
    if ((e.key === "Home" || e.key === "End") && open && items.length > 0) {
      e.preventDefault();
      setActive(e.key === "Home" ? 0 : items.length - 1);
      return;
    }
    if (e.key === "Enter") {
      // Never submit the surrounding form from here.
      e.preventDefault();
      if (!open) openList();
      else if (current !== undefined) commit(current);
      return;
    }
    if (e.key === "Escape" && open) {
      // Close the list, not the drawer around it.
      e.preventDefault();
      e.stopPropagation();
      close();
      return;
    }
    if (e.key === "Tab" && open) close();
  };

  const panel = (
    <div
      ref={panelRef}
      style={style ?? undefined}
      className="fixed z-[60] flex min-h-0 flex-col border border-line-strong bg-surface"
      // Keep focus (and the phone keyboard) in the input while picking.
      onMouseDown={(e) => e.preventDefault()}
    >
      <div
        ref={listRef}
        id={listId}
        role="listbox"
        aria-label={listLabel}
        className="relative min-h-0 flex-1 overscroll-contain overflow-y-auto py-1 empty:hidden"
      >
        {items.map((o, i) => (
          <div
            key={o.value}
            id={optionId(i)}
            role="option"
            aria-selected={i === index}
            onMouseMove={() => setActive(i)}
            onClick={() => commit(o)}
            className={cx(
              "flex min-h-11 cursor-pointer flex-col justify-center gap-0.5 px-3 py-2 md:min-h-0 md:py-1.5",
              i === index && "bg-accent/7 shadow-[inset_2px_0_var(--accent)]",
            )}
          >
            <span className="flex min-w-0 items-center gap-2">
              <span className="min-w-0 truncate text-ui text-fg">
                <Mark text={o.label} range={o.labelMatch} />
              </span>
              {o.chip === undefined ? null : <Chip className="ml-auto shrink-0">{o.chip}</Chip>}
            </span>
            {o.detail === undefined ? null : (
              <span className="truncate font-mono text-meta text-muted">
                <Mark text={o.detail} range={o.detailMatch} />
              </span>
            )}
          </div>
        ))}
      </div>
      {items.length === 0 ? <p className="px-3 py-3 text-small text-muted md:py-2">{emptyText}</p> : null}
      <div className="hidden shrink-0 gap-3 border-t border-line bg-panel px-3 py-1.5 font-mono text-meta-sm text-muted md:flex">
        <span>
          <Kbd>↑↓</Kbd> move
        </span>
        <span>
          <Kbd>↵</Kbd> select
        </span>
        <span>
          <Kbd>Esc</Kbd> close
        </span>
      </div>
    </div>
  );

  return (
    <>
      <input
        ref={inputRef}
        id={id}
        type="text"
        role="combobox"
        aria-label={ariaLabel}
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open && current !== undefined ? optionId(index) : undefined}
        autoComplete="off"
        autoCorrect="off"
        autoCapitalize="off"
        spellCheck={false}
        disabled={disabled}
        size={size}
        data-autofocus={autoFocus ? true : undefined}
        placeholder={placeholder}
        value={query ?? selected?.label ?? ""}
        className={cx(inputClass, "select-chevron cursor-pointer truncate pr-8 focus:cursor-text", className)}
        onPointerDown={(e) => {
          if (document.activeElement !== e.currentTarget) focusingClick.current = true;
          else if (!open) openList();
        }}
        onMouseUp={(e) => {
          if (!focusingClick.current) return;
          focusingClick.current = false;
          e.preventDefault();
        }}
        onFocus={(e) => {
          e.currentTarget.select();
          openList();
        }}
        onBlur={() => {
          focusingClick.current = false;
          close();
        }}
        onChange={(e) => {
          setQuery(e.target.value);
          setOpen(true);
          setActive(0);
        }}
        onKeyDown={onKeyDown}
      />
      {open && style !== null ? createPortal(panel, document.body) : null}
    </>
  );
}
