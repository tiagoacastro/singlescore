import { useMemo, useState, type CSSProperties } from 'react';
import type { Match, MatchCommentary } from '@/graphql/types';
import { FilterChip } from '@/components/FilterChip';
import { kindStyle } from '@/lib/events';
import { formatMinute } from '@/lib/format';
import { colors } from '@/theme';

type Props = {
  match: Pick<Match, 'commentary'>;
};

// A commentary line with a match minute. Untimed lines (lineups
// announced, period start / end) are dropped before display.
type TimedCommentary = MatchCommentary & { minute: number };

// Filter chips; each collects one or more commentary kinds. "Chances"
// includes every attempt plus missed / saved penalties so the user can
// scrub through every chance at a glance.
const FILTER_GROUPS: ReadonlyArray<{
  id: string;
  label: string;
  kinds: ReadonlySet<string>;
  color: string;
}> = [
  { id: 'GOALS', label: 'Goals', kinds: new Set(['GOAL', 'OWN_GOAL']), color: colors.home },
  {
    id: 'CHANCES',
    label: 'Chances',
    kinds: new Set([
      'ATTEMPT_SAVED',
      'ATTEMPT_BLOCKED',
      'ATTEMPT_MISSED',
      'PENALTY_MISSED',
      'PENALTY_SAVED',
    ]),
    color: colors.info,
  },
  { id: 'CARDS', label: 'Cards', kinds: new Set(['YELLOW_CARD', 'RED_CARD']), color: colors.card },
  { id: 'SUBS', label: 'Subs', kinds: new Set(['SUBSTITUTION']), color: colors.textBody },
  {
    id: 'FOULS',
    label: 'Fouls',
    kinds: new Set(['FOUL', 'OFFSIDE', 'FREE_KICK']),
    color: colors.foul,
  },
  { id: 'CORNERS', label: 'Corners', kinds: new Set(['CORNER']), color: colors.corner },
  { id: 'VAR', label: 'VAR', kinds: new Set(['VAR']), color: colors.info },
];

const SIGNIFICANT_KINDS: ReadonlySet<string> = new Set(['GOAL', 'OWN_GOAL', 'RED_CARD']);

// Newest first, like a gamecast: period, minute, stoppage time, then
// ESPN's sequence number.
function sortedDesc<T extends MatchCommentary>(rows: ReadonlyArray<T>): T[] {
  return [...rows].sort(
    (a, b) =>
      (b.period ?? 0) - (a.period ?? 0) ||
      (b.minute ?? 0) - (a.minute ?? 0) ||
      (b.addedMinute ?? 0) - (a.addedMinute ?? 0) ||
      b.sequence - a.sequence,
  );
}

// MatchCommentaryPanel renders the filterable minute-by-minute feed
// scraped from ESPN. Hidden when there are no timed lines (e.g. a match
// that hasn't been scraped yet).
export function MatchCommentaryPanel({ match }: Props) {
  const rows = useMemo(
    () => (match.commentary ?? []).filter((r): r is TimedCommentary => r.minute != null),
    [match.commentary],
  );
  const [activeFilter, setActiveFilter] = useState<string>('ALL');

  const counts = useMemo(() => {
    const c: Record<string, number> = { ALL: rows.length };
    for (const group of FILTER_GROUPS) {
      c[group.id] = rows.filter((r) => group.kinds.has(r.kind)).length;
    }
    return c;
  }, [rows]);

  const filtered = useMemo(() => {
    const group = FILTER_GROUPS.find((g) => g.id === activeFilter);
    return sortedDesc(group ? rows.filter((r) => group.kinds.has(r.kind)) : rows);
  }, [rows, activeFilter]);

  if (rows.length === 0) return null;

  return (
    <section className="card">
      <div className="card__header">
        <h2>Minute-by-minute</h2>
        <span className="card__subtitle">
          {rows.length} line{rows.length === 1 ? '' : 's'} · ESPN
        </span>
      </div>

      <div className="filter-chips">
        <FilterChip
          label="All"
          count={counts.ALL}
          active={activeFilter === 'ALL'}
          color={colors.textSecondary}
          onClick={() => setActiveFilter('ALL')}
        />
        {/* Groups with no rows (no Cards chip in a cardless match) are hidden. */}
        {FILTER_GROUPS.filter((g) => counts[g.id] > 0).map((g) => (
          <FilterChip
            key={g.id}
            label={g.label}
            count={counts[g.id]}
            active={activeFilter === g.id}
            color={g.color}
            onClick={() => setActiveFilter(g.id)}
          />
        ))}
      </div>

      <ul className="feed">
        {filtered.map((row) => {
          const style = kindStyle(row.kind);
          const significant = SIGNIFICANT_KINDS.has(row.kind);
          return (
            <li
              key={row.sequence}
              className={`feed-row${significant ? ' feed-row--significant' : ''}`}
              style={{ '--kind': style.color } as CSSProperties}
            >
              <span className="feed-row__minute">{formatMinute(row.minute, row.addedMinute)}</span>
              <span className="feed-row__kind">{style.short}</span>
              <span className="feed-row__text">{row.text}</span>
              <span className="feed-row__side">{row.teamSide ?? ''}</span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
