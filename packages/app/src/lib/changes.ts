// Edit forms send only what the user changed, compared with a snapshot taken when the form opened, so a value
// another client (an agent, another device) changed meanwhile is not reverted by an untouched field.

/** Structural equality for JSON-like values; an undefined property equals a missing one. */
export function sameValue(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => sameValue(item, b[i]));
  }
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  for (const key of keys) if (!sameValue(Reflect.get(a, key), Reflect.get(b, key))) return false;
  return true;
}

/** The fields of `next` that differ from `baseline`. */
export function changedFields<T extends object>(baseline: T, next: T): Partial<T> {
  const out: Partial<T> = {};
  for (const key in next) {
    if (Object.hasOwn(next, key) && !sameValue(baseline[key], next[key])) out[key] = next[key];
  }
  return out;
}
