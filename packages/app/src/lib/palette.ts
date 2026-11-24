/** The ID a query names ("409" or "#409"), or null when it is not one. */
export function idQuery(query: string): number | null {
  const m = /^#?(\d+)$/.exec(query.trim());
  if (m === null) return null;
  const id = Number(m[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}

export interface PaletteHit<T> {
  item: T;
  /** Matched by its exact ID rather than its title. */
  byId: boolean;
}

/** Palette matches: the record whose ID the query names first, then title matches, at most `limit`. */
export function paletteHits<T extends { id: number; title: string }>(list: readonly T[], query: string, limit: number): PaletteHit<T>[] {
  const q = query.trim().toLowerCase();
  if (q === "") return [];
  const id = idQuery(q);
  const exact = id === null ? undefined : list.find((t) => t.id === id);
  const hits: PaletteHit<T>[] = exact === undefined ? [] : [{ item: exact, byId: true }];
  for (const item of list) {
    if (hits.length >= limit) break;
    if (item !== exact && item.title.toLowerCase().includes(q)) hits.push({ item, byId: false });
  }
  return hits.slice(0, limit);
}
