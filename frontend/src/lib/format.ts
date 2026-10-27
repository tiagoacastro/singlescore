// Display formatters shared across pages and components.

export function formatPct(p: number, digits = 0): string {
  return `${(p * 100).toFixed(digits)}%`;
}

export function formatNumber(n: number | null | undefined, digits = 3): string {
  return n == null || Number.isNaN(n) ? '—' : n.toFixed(digits);
}

// Match clock label: 36', 45'+3, or ~60' for an approximate minute.
export function formatMinute(
  minute: number | null | undefined,
  added?: number | null,
  approximate = false,
): string {
  if (minute == null) return '';
  if (approximate) return `~${minute}'`;
  return added ? `${minute}'+${added}` : `${minute}'`;
}
