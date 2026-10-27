import { colors } from '@/theme';

// One entry per match_events.event_type / match_commentary.kind, shared
// by the event timeline, the commentary feed and the opportunity map.
export type KindStyle = {
  label: string; // full name, e.g. "Yellow card"
  short: string; // compact feed label, e.g. "Yellow"
  color: string;
  icon?: string; // plain unicode so it renders without a webfont
  shotRadius?: number; // set for kinds plotted on the opportunity map
};

export const EVENT_KINDS: Record<string, KindStyle> = {
  GOAL: { label: 'Goal', short: 'Goal', color: colors.home, icon: '⚽', shotRadius: 7 },
  OWN_GOAL: { label: 'Own goal', short: 'Own goal', color: colors.away, icon: '⚽', shotRadius: 7 },
  PENALTY_MISSED: { label: 'Penalty missed', short: 'Pen. miss', color: colors.danger, icon: '✕', shotRadius: 6 },
  PENALTY_SAVED: { label: 'Penalty saved', short: 'Pen. save', color: colors.card, shotRadius: 6 },
  ATTEMPT_SAVED: { label: 'Shot (saved)', short: 'Shot saved', color: colors.info, shotRadius: 4.5 },
  ATTEMPT_BLOCKED: { label: 'Shot (blocked)', short: 'Shot blocked', color: colors.textSecondary, shotRadius: 4 },
  ATTEMPT_MISSED: { label: 'Shot (off target)', short: 'Shot off', color: colors.textMuted, shotRadius: 3.5 },
  YELLOW_CARD: { label: 'Yellow card', short: 'Yellow', color: colors.card, icon: '🟨' },
  RED_CARD: { label: 'Red card', short: 'Red', color: colors.danger, icon: '🟥' },
  SUBSTITUTION: { label: 'Substitution', short: 'Sub', color: colors.textBody, icon: '↔' },
  VAR: { label: 'VAR', short: 'VAR', color: colors.info, icon: '📺' },
  FOUL: { label: 'Foul', short: 'Foul', color: colors.foul },
  OFFSIDE: { label: 'Offside', short: 'Offside', color: colors.foul },
  FREE_KICK: { label: 'Free kick', short: 'Free kick', color: colors.textSecondary },
  CORNER: { label: 'Corner', short: 'Corner', color: colors.corner },
  PERIOD_START: { label: 'Period', short: 'Period', color: colors.textMuted },
  PERIOD_END: { label: 'Period', short: 'Period', color: colors.textMuted },
  LINEUP: { label: 'Lineup', short: 'Lineup', color: colors.textMuted },
  COMMENTARY: { label: 'Note', short: 'Note', color: colors.textMuted },
};

// Style for any kind, falling back to a title-cased label for kinds ESPN
// adds later.
export function kindStyle(kind: string): KindStyle {
  const known = EVENT_KINDS[kind];
  if (known) return known;
  const spaced = kind.replace(/_/g, ' ').toLowerCase();
  const label = spaced.charAt(0).toUpperCase() + spaced.slice(1);
  return { label, short: label, color: colors.textSecondary, icon: '•' };
}

export const SHOT_KINDS: ReadonlySet<string> = new Set(
  Object.keys(EVENT_KINDS).filter((k) => EVENT_KINDS[k].shotRadius != null),
);
