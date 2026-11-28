import { useEffect, useState } from "react";

export function usePhoneActionBar() {
  const [bar, setBar] = useState<HTMLDivElement | null>(null);
  const [height, setHeight] = useState(0);
  useEffect(() => {
    if (bar === null) return;
    const observer = new ResizeObserver(() => setHeight(bar.offsetHeight));
    observer.observe(bar, { box: "border-box" });
    return () => observer.disconnect();
  }, [bar]);
  return {
    barProps: {
      ref: setBar,
      className: "fixed inset-x-0 bottom-[calc(3.5rem+env(safe-area-inset-bottom))] z-30 flex max-h-[45dvh] flex-wrap items-center gap-2 overflow-y-auto border-t border-line bg-bg py-2.5 pr-[max(0.75rem,env(safe-area-inset-right))] pl-[max(0.75rem,env(safe-area-inset-left))] md:contents",
    },
    spacer: bar === null ? null : <div aria-hidden className="md:hidden" style={{ height }} />,
  };
}
