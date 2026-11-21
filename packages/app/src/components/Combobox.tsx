import { useCallback, useId, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { SEARCH_THRESHOLD, edgeIndex, filterOptions, stepIndex, typeaheadIndex, type ComboboxOption } from "../lib/combobox.ts";
import type { MatchRange } from "../lib/projects.ts";
import { IconCheck } from "./icons.tsx";
import { Chip, Kbd, activeRowClass, controlClass, cx } from "./ui.tsx";

export type { ComboboxOption };

const GAP = 4;
const EDGE = 8;
const MIN_WIDTH = 280;
const MAX_HEIGHT = 344;
/** Below this much room under the control, flip above when there is more room there. */
const FLIP_BELOW = 200;
/** Type-ahead keys within this long build one prefix. */
const TYPEAHEAD_MS = 500;

/** Fixed box under the control (above it near the bottom of the visible area, e.g. over a phone keyboard). */
function placement(anchor: HTMLElement): CSSProperties {
  const rect = anchor.getBoundingClientRect();
  const vv = window.visualViewport;
  const viewTop = vv?.offsetTop ?? 0;
  const viewBottom = viewTop + (vv?.height ?? window.innerHeight);
  const viewWidth = document.documentElement.clientWidth;
  const width = Math.min(Math.max(rect.width, MIN_WIDTH), viewWidth - 2 * EDGE);
  // Left-aligned with the control; right-aligned when wider than the room to its right.
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
 * The one dropdown (ARIA 1.2 combobox). Searchable: an input where focus or click opens the list with the text
 * selected, typing filters, ↑↓ move, Enter commits, Esc/Tab/blur restore the selection. Select-only (short lists, no
 * phone keyboard): a button where Enter/Space/↓ open, typing jumps to a match, Enter/Space commit. Only listed,
 * enabled values can be chosen.
 */
export function Combobox({
  id,
  value,
  onChange,
  options,
  pinned = [],
  searchable,
  placeholder,
  emptyText = "No matches",
  listLabel,
  disabled = false,
  small = false,
  className,
  size,
  autoFocus = false,
  leading,
  showTag = false,
  "aria-label": ariaLabel,
}: {
  id?: string | undefined;
  value: string;
  onChange: (value: string) => void;
  /** The options in order (filtered with `filterOptions`), or a function ranking those matching `query` (`""` lists all). */
  options: readonly ComboboxOption[] | ((query: string) => ComboboxOption[]);
  /** Listed first while the query is empty (e.g. "All projects"). */
  pinned?: readonly ComboboxOption[] | undefined;
  /** Type-to-filter input; defaults to lists longer than `SEARCH_THRESHOLD`. */
  searchable?: boolean | undefined;
  /** Shown when `value` matches no option. */
  placeholder?: string | undefined;
  emptyText?: ReactNode;
  listLabel: string;
  disabled?: boolean | undefined;
  /** Compact control for dense panels. */
  small?: boolean | undefined;
  className?: string | undefined;
  /** Intrinsic width in characters (where the layout does not stretch the input). */
  size?: number | undefined;
  /** Marks the control `data-autofocus` so an enclosing Drawer focuses it first. */
  autoFocus?: boolean | undefined;
  /** Shown before the selected label in a select-only trigger (e.g. a state marker). */
  leading?: ReactNode;
  /** A select-only trigger also shows the selected option's tag (e.g. a connection's machine). */
  showTag?: boolean | undefined;
  "aria-label"?: string | undefined;
}) {
  const listId = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  /** Set on a pointer press that focuses the input, so its mouseup does not clear the text selection. */
  const focusingClick = useRef(false);
  const typeahead = useRef({ text: "", at: 0 });
  const [open, setOpen] = useState(false);
  /** Typed text; null shows the selected option's label. */
  const [query, setQuery] = useState<string | null>(null);
  const [active, setActive] = useState(0);
  const [style, setStyle] = useState<CSSProperties | null>(null);

  const match = useMemo(
    () => (typeof options === "function" ? options : (q: string) => filterOptions(options, q)),
    [options],
  );
  const all = useMemo(() => [...pinned, ...match("")], [pinned, match]);
  const search = searchable ?? all.length > SEARCH_THRESHOLD;
  const selected = all.find((o) => o.value === value);
  const typed = search ? (query ?? "") : "";
  const items = useMemo(() => (typed.trim() === "" ? all : match(typed)), [typed, all, match]);
  const index = Math.min(active, items.length - 1);
  const current = items[index];
  const optionId = (i: number) => `${listId}-${i}`;

  const openList = () => {
    setOpen(true);
    const at = all.findIndex((o) => o.value === value);
    setActive(at >= 0 ? at : Math.max(0, edgeIndex(all, "first")));
  };
  const close = useCallback(() => {
    setOpen(false);
    setQuery(null);
  }, []);
  const commit = (o: ComboboxOption) => {
    if (o.disabled === true) return;
    if (o.value !== value) onChange(o.value);
    close();
  };

  useLayoutEffect(() => {
    const anchor = search ? inputRef.current : buttonRef.current;
    if (!open || anchor === null) {
      setStyle(null);
      return;
    }
    const update = () => setStyle(placement(anchor));
    // Scrolling the list itself must not re-place it.
    const onScroll = (e: Event) => {
      if (e.target instanceof Node && panelRef.current?.contains(e.target)) return;
      update();
    };
    // Safari can open a button by tap without focusing it, so blur alone cannot dismiss the list.
    const onOutside = (e: Event) => {
      const target = e.target;
      if (!(target instanceof Node) || anchor.contains(target) || panelRef.current?.contains(target)) return;
      close();
    };
    update();
    const vv = window.visualViewport;
    document.addEventListener("pointerdown", onOutside, true);
    document.addEventListener("focusin", onOutside, true);
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", update);
    vv?.addEventListener("resize", update);
    vv?.addEventListener("scroll", update);
    return () => {
      document.removeEventListener("pointerdown", onOutside, true);
      document.removeEventListener("focusin", onOutside, true);
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", update);
      vv?.removeEventListener("resize", update);
      vv?.removeEventListener("scroll", update);
    };
  }, [open, close, search]);

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

  /** Keys both modes share; true when handled. */
  const navigate = (e: KeyboardEvent<HTMLElement>): boolean => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!open) openList();
      else if (items.length > 0) setActive(stepIndex(items, index, e.key === "ArrowDown" ? 1 : -1));
      return true;
    }
    if (e.altKey) return false;
    if ((e.key === "Home" || e.key === "End") && open && items.length > 0) {
      e.preventDefault();
      setActive(Math.max(0, edgeIndex(items, e.key === "Home" ? "first" : "last")));
      return true;
    }
    if (e.key === "Escape" && open) {
      // Close the list, not the drawer around it.
      e.preventDefault();
      e.stopPropagation();
      close();
      return true;
    }
    if (e.key === "Tab" && open) close();
    return false;
  };

  const onInputKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.ctrlKey || e.metaKey || navigate(e)) return;
    if (e.key === "Enter") {
      // Never submit the surrounding form from here.
      e.preventDefault();
      if (!open) openList();
      else if (current !== undefined) commit(current);
    }
  };

  const onButtonKeyDown = (e: KeyboardEvent<HTMLButtonElement>) => {
    if (e.ctrlKey || e.metaKey || navigate(e) || e.altKey) return;
    const now = Date.now();
    const ahead = typeahead.current;
    const building = ahead.text !== "" && now - ahead.at < TYPEAHEAD_MS;
    // Space continues a type-ahead prefix; otherwise it acts like Enter.
    if (e.key === "Enter" || (e.key === " " && !building)) {
      // Never click the button (that toggles) or submit the surrounding form.
      e.preventDefault();
      if (!open) openList();
      else if (current !== undefined) commit(current);
      return;
    }
    if (e.key.length !== 1) return;
    e.preventDefault();
    const text = (building ? ahead.text : "") + e.key;
    typeahead.current = { text, at: now };
    const from = open ? index : all.findIndex((o) => o.value === value);
    const hit = typeaheadIndex(all, text, from);
    if (!open) setOpen(true);
    if (hit >= 0) setActive(hit);
    else if (!open) openList();
  };

  const panel = (
    <div
      ref={panelRef}
      style={style ?? undefined}
      className="fixed z-[60] flex min-h-0 flex-col border border-line-strong bg-surface"
      // Keep focus (and any phone keyboard) on the control while picking.
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
            aria-disabled={o.disabled === true ? true : undefined}
            onMouseMove={() => {
              if (o.disabled !== true) setActive(i);
            }}
            onClick={() => commit(o)}
            className={cx(
              "flex min-h-11 gap-2 py-2 pr-3 pl-2 md:min-h-0 md:py-1.5",
              o.disabled === true ? "cursor-default" : "cursor-pointer",
              i === index && o.disabled !== true && activeRowClass,
            )}
          >
            <span className={cx("flex h-5 w-3.5 shrink-0 items-center text-fg", o.detail === undefined && "self-center")}>
              {o.value === value && o.disabled !== true ? <IconCheck size={14} /> : null}
            </span>
            <span className="flex min-w-0 flex-1 flex-col justify-center gap-0.5">
              <span className="flex min-w-0 items-center gap-2">
                <span className={cx("min-w-0 truncate text-ui", o.disabled === true ? "text-muted" : "text-fg")}>
                  <Mark text={o.label} range={o.labelMatch} />
                </span>
                {o.chip === undefined && o.tag === undefined ? null : (
                  <span className="ml-auto flex shrink-0 items-center gap-2">
                    {o.chip === undefined ? null : <Chip>{o.chip}</Chip>}
                    {o.tag === undefined ? null : (
                      <span className="font-mono text-meta text-muted">
                        <Mark text={o.tag} range={o.tagMatch} />
                      </span>
                    )}
                  </span>
                )}
              </span>
              {o.detail === undefined ? null : (
                <span className="truncate font-mono text-meta text-muted">
                  <Mark text={o.detail} range={o.detailMatch} />
                </span>
              )}
            </span>
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

  const shared = {
    id,
    role: "combobox",
    "aria-label": ariaLabel,
    "aria-expanded": open,
    "aria-controls": open ? listId : undefined,
    "aria-activedescendant": open && current !== undefined ? optionId(index) : undefined,
    disabled,
    "data-autofocus": autoFocus ? true : undefined,
  };

  return (
    <>
      {search ? (
        <input
          {...shared}
          ref={inputRef}
          type="text"
          aria-autocomplete="list"
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="off"
          spellCheck={false}
          size={size}
          placeholder={placeholder}
          value={query ?? selected?.label ?? ""}
          className={cx(controlClass(small), "select-chevron cursor-pointer truncate pr-8 focus:cursor-text", className)}
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
          onKeyDown={onInputKeyDown}
        />
      ) : (
        <button
          {...shared}
          ref={buttonRef}
          type="button"
          aria-haspopup="listbox"
          className={cx(
            controlClass(small),
            "select-chevron min-w-0 cursor-pointer truncate pr-8 text-left disabled:cursor-default",
            className,
          )}
          onClick={() => (open ? close() : openList())}
          onBlur={close}
          onKeyDown={onButtonKeyDown}
        >
          {selected === undefined ? (
            <span className="text-muted">{placeholder ?? " "}</span>
          ) : showTag && selected.tag !== undefined ? (
            <span className="flex min-w-0 items-baseline gap-2">
              {leading}
              <span className="min-w-0 truncate">{selected.label}</span>
              <span className="ml-auto min-w-0 truncate font-mono text-meta text-muted">{selected.tag}</span>
            </span>
          ) : leading === undefined ? (
            selected.label
          ) : (
            <span className="flex min-w-0 items-center gap-2">
              {leading}
              <span className="min-w-0 truncate">{selected.label}</span>
            </span>
          )}
        </button>
      )}
      {open && style !== null ? createPortal(panel, document.body) : null}
    </>
  );
}
