import { useParams, useSearchParams } from "react-router";

/** Numeric `:id` route param, or null when missing / not a positive integer. */
export function useIdParam(): number | null {
  const { id } = useParams();
  const n = Number(id);
  return Number.isInteger(n) && n > 0 ? n : null;
}

/**
 * One search param as view state (a list filter), so Back and a shared link keep it. Setting `fallback` (the
 * default) removes it from the address; changes replace the history entry.
 */
export function useSearchParam<T extends string>(key: string, allowed: readonly T[], fallback: T): [T, (value: T) => void] {
  const [params, setParams] = useSearchParams();
  const value = allowed.find((item) => item === params.get(key)) ?? fallback;
  const set = (next: T) =>
    setParams(
      (prev) => {
        const out = new URLSearchParams(prev);
        if (next === fallback) out.delete(key);
        else out.set(key, next);
        return out;
      },
      { replace: true },
    );
  return [value, set];
}
