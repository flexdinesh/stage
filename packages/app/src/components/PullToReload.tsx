import { useEffect, useRef, useState, type RefObject } from "react";
import { PullToReloadGesture, type PullPhase } from "../lib/pull-to-reload.ts";

const CONTROLS = 'a, button, input, textarea, select, label, summary, audio, video, [contenteditable]:not([contenteditable="false"]), [role="button"], [role="link"], [role="textbox"], [role="combobox"], [role="slider"], [role="listbox"], [role="option"]';
const EDITORS = 'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"], [role="combobox"]';

function blocked(): boolean {
  return window.scrollY > 0 || document.querySelector('[aria-modal="true"]') !== null ||
    document.activeElement?.closest(EDITORS) != null || (window.visualViewport?.scale ?? 1) !== 1;
}

function eligible(target: EventTarget | null): target is Element {
  if (!(target instanceof Element) || target.closest(CONTROLS) !== null) return false;
  // A nested scroll surface owns its gestures, including when already at its edge.
  for (let el: Element | null = target; el !== null && el !== document.body && el !== document.documentElement; el = el.parentElement) {
    const style = getComputedStyle(el);
    if ((/auto|scroll/.test(style.overflowY) && el.scrollHeight > el.clientHeight) ||
      (/auto|scroll/.test(style.overflowX) && el.scrollWidth > el.clientWidth)) return false;
  }
  return true;
}

const LABELS: Record<PullPhase, string> = {
  idle: "",
  pulling: "Pull to reload",
  armed: "Release to reload",
  reloading: "Reloading…",
};

/** Mounted with the router location as key, so navigation cancels even a scheduled reload. */
export function PullToReload({ contentRef }: { contentRef: RefObject<HTMLElement | null> }) {
  const [phase, setPhase] = useState<PullPhase>("idle");
  const targetRef = useRef<Element | null>(null);
  const frameRef = useRef<number | undefined>(undefined);

  useEffect(() => {
    const gesture = new PullToReloadGesture();
    const content = contentRef.current;
    const href = window.location.href;

    function paint() {
      if (gesture.phase === "pulling" || gesture.phase === "armed") {
        content?.setAttribute("data-pulling", "");
      } else {
        content?.removeAttribute("data-pulling");
      }
      content?.style.setProperty("--pull-offset", `${gesture.offset}px`);
      setPhase(gesture.phase);
    }

    function cancel() {
      gesture.cancel();
      targetRef.current = null;
      if (frameRef.current !== undefined) cancelAnimationFrame(frameRef.current);
      frameRef.current = undefined;
      paint();
    }

    function start(event: TouchEvent) {
      if (gesture.phase === "reloading") return;
      cancel();
      const touch = event.touches.item(0);
      if (event.defaultPrevented || event.touches.length !== 1 || touch === null || blocked() || !eligible(event.target)) return;
      targetRef.current = event.target;
      gesture.start(touch);
    }

    function move(event: TouchEvent) {
      if (targetRef.current === null) return;
      const touch = event.touches.item(0);
      if (event.defaultPrevented || event.touches.length !== 1 || touch === null || blocked()) {
        cancel();
        return;
      }
      if (gesture.move(touch)) {
        // Fallback for browsers without root overscroll support; never swallow other gestures.
        if (!event.cancelable) {
          cancel();
          return;
        }
        event.preventDefault();
      }
      paint();
    }

    function end(event: TouchEvent) {
      if (targetRef.current === null) return;
      const touch = event.changedTouches.item(0);
      if (event.defaultPrevented || event.touches.length !== 0 || touch === null || blocked() || !eligible(targetRef.current)) {
        cancel();
        return;
      }
      targetRef.current = null;
      const reload = gesture.release(touch);
      paint();
      if (!reload) return;
      // Let the busy indicator paint, then reload the current URL once.
      frameRef.current = requestAnimationFrame(() => {
        frameRef.current = requestAnimationFrame(() => {
          if (blocked() || window.location.href !== href) {
            cancel();
            return;
          }
          window.location.reload();
        });
      });
    }

    document.addEventListener("touchstart", start, { passive: true });
    document.addEventListener("touchmove", move, { passive: false });
    document.addEventListener("touchend", end, { passive: true });
    document.addEventListener("touchcancel", cancel, { passive: true });
    document.addEventListener("focusin", cancel);
    document.addEventListener("visibilitychange", cancel);
    window.addEventListener("resize", cancel);
    return () => {
      if (frameRef.current !== undefined) cancelAnimationFrame(frameRef.current);
      document.removeEventListener("touchstart", start);
      document.removeEventListener("touchmove", move);
      document.removeEventListener("touchend", end);
      document.removeEventListener("touchcancel", cancel);
      document.removeEventListener("focusin", cancel);
      document.removeEventListener("visibilitychange", cancel);
      window.removeEventListener("resize", cancel);
      content?.removeAttribute("data-pulling");
      content?.style.removeProperty("--pull-offset");
    };
  }, [contentRef]);

  return (
    <div
      role="status"
      aria-live="polite"
      aria-atomic="true"
      className="pointer-events-none fixed inset-x-0 top-[calc(3.25rem+var(--inset-top))] z-40 flex justify-center pr-[env(safe-area-inset-right)] pl-[env(safe-area-inset-left)] lg:top-0"
    >
      {phase === "idle" ? null : (
        <div className="mt-2 flex items-center gap-2 border border-line-strong bg-surface px-3 py-2 text-small text-fg">
          {phase === "reloading" ? <span aria-hidden="true" className="size-3 border-[1.5px] border-current border-t-transparent motion-safe:animate-spin" /> : null}
          {LABELS[phase]}
        </div>
      )}
    </div>
  );
}
