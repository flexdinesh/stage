import { useParams } from "react-router";

/** Numeric `:id` route param, or null when missing / not a positive integer. */
export function useIdParam(): number | null {
  const { id } = useParams();
  const n = Number(id);
  return Number.isInteger(n) && n > 0 ? n : null;
}
