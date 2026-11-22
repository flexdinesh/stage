import { useEffect, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { IconClose } from "../icons.tsx";
import { Button } from "./button.tsx";
import { cx } from "./cx.ts";
import { Kbd } from "./tone.tsx";

const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';

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
  const panel = useRef<HTMLDivElement>(null);
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
  }, []);

  return createPortal(
    <div className="fixed inset-0 z-50">
      <div className="absolute inset-0 bg-scrim" onClick={onClose} />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={cx(
          "absolute inset-0 flex flex-col bg-bg md:inset-y-0 md:right-0 md:left-auto md:w-full md:border-l md:border-line-strong",
          wide ? "md:max-w-[680px]" : "md:max-w-[580px]",
        )}
      >
        <div className="flex h-[calc(3.25rem+var(--inset-top))] shrink-0 items-center gap-3 border-b border-line pt-[var(--inset-top)] pr-[max(1rem,env(safe-area-inset-right))] pl-[max(1rem,env(safe-area-inset-left))] md:h-12 md:pt-0 md:pr-4 md:pl-4">
          <h2 className="text-title font-semibold">{title}</h2>
          <div className="ml-auto flex items-center gap-3">
            <Kbd className="max-md:hidden">Esc</Kbd>
            <Button variant="ghost" small onClick={onClose} aria-label="Close">
              <IconClose />
            </Button>
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
      </div>
    </div>,
    document.body,
  );
}
