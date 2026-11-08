import { useCallback, useId, useLayoutEffect, useMemo, useRef, useState, type ComponentProps, type CSSProperties, type KeyboardEvent, type Ref } from "react";
import { createPortal } from "react-dom";
import { useProjects, useSnippets } from "../api/queries.ts";
import type { Snippet } from "../api/types.ts";
import { findTrigger, matchSnippets, snippetsFor } from "../lib/snippets.ts";
import { Chip, Kbd, Textarea, cx } from "./ui.tsx";

const MIRRORED = [
  "box-sizing",
  "font-family",
  "font-size",
  "font-weight",
  "font-style",
  "letter-spacing",
  "line-height",
  "text-transform",
  "text-indent",
  "word-spacing",
  "tab-size",
  "padding-top",
  "padding-right",
  "padding-bottom",
  "padding-left",
  "border-top-width",
  "border-right-width",
  "border-bottom-width",
  "border-left-width",
  "border-style",
] as const;

/** Caret offset of `index` inside the textarea's content box, via a hidden mirror div with the same text metrics. */
function caretOffset(el: HTMLTextAreaElement, index: number): { left: number; top: number; height: number } {
  const cs = getComputedStyle(el);
  const div = document.createElement("div");
  for (const p of MIRRORED) div.style.setProperty(p, cs.getPropertyValue(p));
  const borders = parseFloat(cs.borderLeftWidth) + parseFloat(cs.borderRightWidth);
  Object.assign(div.style, {
    position: "absolute",
    visibility: "hidden",
    top: "0",
    left: "-9999px",
    width: `${el.clientWidth + borders}px`,
    whiteSpace: "pre-wrap",
    overflowWrap: "break-word",
  });
  div.textContent = el.value.slice(0, index);
  const mark = document.createElement("span");
  mark.textContent = el.value.slice(index) || ".";
  div.append(mark);
  document.body.append(div);
  const lineHeight = parseFloat(cs.lineHeight);
  const out = { left: mark.offsetLeft, top: mark.offsetTop, height: Number.isNaN(lineHeight) ? mark.offsetHeight : lineHeight };
  div.remove();
  return out;
}

const PICKER_WIDTH = 360;
const GAP = 4;
const EDGE = 8;

/** Laptop: under the caret (flips above near the bottom). Phone: full width under the textarea. */
function pickerPosition(el: HTMLTextAreaElement, index: number): CSSProperties {
  const rect = el.getBoundingClientRect();
  const viewH = window.visualViewport?.height ?? window.innerHeight;
  let left: number;
  let width: number;
  let below: number;
  let above: number;
  if (window.matchMedia("(min-width: 768px)").matches) {
    const c = caretOffset(el, index);
    const top = rect.top + c.top - el.scrollTop;
    width = PICKER_WIDTH;
    left = Math.max(EDGE, Math.min(rect.left + c.left - el.scrollLeft, window.innerWidth - width - EDGE));
    below = top + c.height + GAP;
    above = top - GAP;
  } else {
    left = rect.left;
    width = rect.width;
    below = rect.bottom + GAP;
    above = rect.top - GAP;
  }
  const spaceBelow = viewH - below - EDGE;
  const spaceAbove = above - EDGE;
  if (spaceBelow < 240 && spaceAbove > spaceBelow) {
    return { left, width, bottom: window.innerHeight - above, maxHeight: spaceAbove };
  }
  return { left, width, top: below, maxHeight: spaceBelow };
}

type Props = Omit<ComponentProps<"textarea">, "value" | "onChange" | "ref"> & {
  value: string;
  onValueChange: (value: string) => void;
  /** Scope: global snippets plus this project's (global only when null). */
  projectId: number | null;
  ref?: Ref<HTMLTextAreaElement>;
};

/**
 * Textarea with snippet insertion: typing `/` at a word start (or `template:`) opens a picker of matching snippets;
 * ↑↓ move, Enter/Tab insert, Esc closes. Inserting replaces the trigger token and keeps native undo.
 */
export function SnippetTextarea({ value, onValueChange, projectId, ref, onKeyDown, onBlur, onSelect, ...rest }: Props) {
  const el = useRef<HTMLTextAreaElement | null>(null);
  const listId = useId();
  const snippets = useSnippets();
  const projects = useProjects("all");
  /** Caret index while focused; null when blurred. */
  const [caret, setCaret] = useState<number | null>(null);
  /** Trigger start the user dismissed with Esc (stays closed until the token ends). */
  const [dismissed, setDismissed] = useState<number | null>(null);
  const [active, setActive] = useState(0);
  const [pos, setPos] = useState<CSSProperties | null>(null);

  const scope = useMemo(() => snippetsFor(snippets.data?.snippets ?? [], projectId), [snippets.data, projectId]);
  const trigger = caret === null || scope.length === 0 ? null : findTrigger(value, caret);
  const matches = trigger === null ? [] : matchSnippets(scope, trigger.query);
  const open = trigger !== null && trigger.start !== dismissed && matches.length > 0;
  const index = Math.min(active, matches.length - 1);
  const current = matches[index];
  const triggerStart = trigger?.start ?? null;

  const projectTitle = useMemo(() => {
    const m = new Map<number, string>();
    for (const p of projects.data?.projects ?? []) m.set(p.id, p.title);
    return m;
  }, [projects.data]);

  const setRef = useCallback(
    (node: HTMLTextAreaElement | null) => {
      el.current = node;
      if (typeof ref === "function") ref(node);
      else if (ref) ref.current = node;
    },
    [ref],
  );

  useLayoutEffect(() => {
    const node = el.current;
    if (!open || node === null || triggerStart === null) {
      setPos(null);
      return;
    }
    const update = () => setPos(pickerPosition(node, triggerStart));
    update();
    window.addEventListener("scroll", update, true);
    window.addEventListener("resize", update);
    window.visualViewport?.addEventListener("resize", update);
    return () => {
      window.removeEventListener("scroll", update, true);
      window.removeEventListener("resize", update);
      window.visualViewport?.removeEventListener("resize", update);
    };
  }, [open, triggerStart]);

  const syncCaret = (node: HTMLTextAreaElement) => {
    const at = node.selectionStart === node.selectionEnd ? node.selectionStart : null;
    setCaret(at);
    const t = at === null ? null : findTrigger(node.value, at);
    setDismissed((prev) => (t === null || t.start !== prev ? null : prev));
  };

  const insert = (s: Snippet) => {
    const node = el.current;
    if (node === null || trigger === null) return;
    const next = value.slice(0, trigger.start) + s.body + value.slice(trigger.end);
    const at = trigger.start + s.body.length;
    node.focus();
    node.setSelectionRange(trigger.start, trigger.end);
    // execCommand keeps the native undo stack; fall back to a plain value update where it is unsupported.
    if (!document.execCommand("insertText", false, s.body)) {
      onValueChange(next);
      requestAnimationFrame(() => node.setSelectionRange(at, at));
    }
    setCaret(at);
    setActive(0);
    // Don't reopen on a trigger-like token at the end of the inserted text.
    const after = findTrigger(next, at);
    setDismissed(after === null ? null : after.start);
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (open && trigger !== null && current !== undefined && !e.ctrlKey && !e.metaKey && !e.altKey) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const step = e.key === "ArrowDown" ? 1 : -1;
        setActive((index + step + matches.length) % matches.length);
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        insert(current);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setDismissed(trigger.start);
        return;
      }
    }
    onKeyDown?.(e);
  };

  const optionId = (s: Snippet) => `${listId}-${s.id}`;

  return (
    <>
      <Textarea
        {...rest}
        ref={setRef}
        value={value}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open && current !== undefined ? optionId(current) : undefined}
        onChange={(e) => {
          onValueChange(e.target.value);
          setActive(0);
          syncCaret(e.target);
        }}
        onSelect={(e) => {
          syncCaret(e.currentTarget);
          onSelect?.(e);
        }}
        onFocus={(e) => {
          syncCaret(e.currentTarget);
          rest.onFocus?.(e);
        }}
        onBlur={(e) => {
          setCaret(null);
          onBlur?.(e);
        }}
        onKeyDown={handleKeyDown}
      />
      {open && pos !== null
        ? createPortal(
            <div
              style={pos}
              className="fixed z-[60] flex flex-col border border-line-strong bg-surface"
              // Keep focus (and the caret) in the textarea while picking.
              onMouseDown={(e) => e.preventDefault()}
            >
              <div className="flex h-7 shrink-0 items-center gap-2 border-b border-line bg-panel px-3">
                <span className="label-caps text-fg">Snippets</span>
                <span className="font-mono text-[11px] text-muted">{matches.length}</span>
              </div>
              <div id={listId} role="listbox" aria-label="Snippets" className="min-h-0 flex-1 overflow-y-auto py-1">
                {matches.map((s, i) => (
                  <div
                    key={s.id}
                    id={optionId(s)}
                    role="option"
                    aria-selected={i === index}
                    ref={i === index ? (node) => node?.scrollIntoView({ block: "nearest" }) : undefined}
                    onMouseMove={() => setActive(i)}
                    onClick={() => insert(s)}
                    className={cx(
                      "flex cursor-pointer flex-col gap-0.5 px-3 py-1.5",
                      i === index && "bg-accent/7 shadow-[inset_2px_0_var(--accent)]",
                    )}
                  >
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="min-w-0 truncate text-[13px] text-fg">{s.name}</span>
                      <Chip className="ml-auto max-w-[45%] shrink-0 truncate">
                        {s.projectId === null ? "global" : (projectTitle.get(s.projectId) ?? `project ${s.projectId}`)}
                      </Chip>
                    </span>
                    <span className="truncate font-mono text-[11.5px] text-muted">{s.body}</span>
                  </div>
                ))}
              </div>
              <div className="hidden shrink-0 gap-3 border-t border-line bg-panel px-3 py-1.5 font-mono text-[11px] text-muted md:flex">
                <span>
                  <Kbd>↑↓</Kbd> move
                </span>
                <span>
                  <Kbd>↵</Kbd> insert
                </span>
                <span>
                  <Kbd>Esc</Kbd> close
                </span>
              </div>
            </div>,
            document.body,
          )
        : null}
    </>
  );
}
