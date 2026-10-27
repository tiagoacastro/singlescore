// JS mirror of styles/_tokens.scss for the places SCSS can't reach:
// Recharts props, SVG attributes and per-kind colours passed to CSS as
// custom properties. Keep the two in sync.
export const colors = {
  bgRaised: '#141924',
  border: '#1f2735',
  textPrimary: '#e7ecf3',
  textBody: '#c4ccd9',
  textSecondary: '#9aa6ba',
  textMuted: '#6c7891',
  home: '#38d996',
  draw: '#9aa6ba',
  away: '#ff7fa3',
  accent: '#4c9aff',
  info: '#7fb5ff',
  card: '#f2c94c',
  danger: '#ef4a56',
  foul: '#ff9f80',
  corner: '#9b7fff',
} as const;

// Colour for a team side, with a neutral fallback.
export function sideColor(side: string | null | undefined): string {
  return side === 'HOME' ? colors.home : side === 'AWAY' ? colors.away : colors.textSecondary;
}
