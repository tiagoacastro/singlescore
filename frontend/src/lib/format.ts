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

// "45s ago", "12m ago", "3h ago", or the date for anything older.
export function formatRelative(iso: string | null | undefined): string {
  if (!iso) return 'never';
  const sec = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr}h ago`;
  return new Date(iso).toLocaleDateString();
}
