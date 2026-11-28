import { useEffect, useState } from "react";

/** The current time, refreshed every `intervalMs` while `enabled` (countdowns and relative times that must move on their own). */
export function useNow(intervalMs: number, enabled = true): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!enabled) return;
    const tick = () => setNow(Date.now());
    tick();
    const t = setInterval(tick, intervalMs);
    return () => clearInterval(t);
  }, [intervalMs, enabled]);
  return now;
}
