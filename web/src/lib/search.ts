export const parseSearch = (raw: string): Record<string, string> =>
  Object.fromEntries(new URLSearchParams(raw));
export function stringifySearch(values: Record<string, unknown>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(values)) {
    if (typeof v === "string" && v !== "") q.set(k, v);
  }
  return q.size ? "?" + q.toString() : "";
}
