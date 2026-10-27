import { useMemo, useState } from 'react';
import type { Match, MatchCommentary } from '@/graphql/types';
import { formatMinute } from '@/lib/format';

type Props = {
  match: Pick<Match, 'commentary' | 'homeTeam' | 'awayTeam'>;
};

// A commentary line with a match minute. Untimed lines (lineups
// announced, period start / end) are dropped before display.
type TimedCommentary = MatchCommentary & { minute: number };

// Grouping used by the filter-chip row. "ALL" is the implicit default.
// Each group collects one or more kinds from the raw ESPN
// classification; e.g. "Goals" includes both GOAL and OWN_GOAL,
// "Chances" includes every ATTEMPT_* kind plus missed / saved penalties
// so the user can scrub through every chance at a glance.
const FILTER_GROUPS: ReadonlyArray<{
  id: string;
  label: string;
  kinds: ReadonlySet<string>;
  color: string;
}> = [
  {
    id: 'GOALS',
    label: 'Goals',
    kinds: new Set(['GOAL', 'OWN_GOAL']),
    color: '#38d996',
  },
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
    color: '#7fb5ff',
  },
  {
    id: 'CARDS',
    label: 'Cards',
    kinds: new Set(['YELLOW_CARD', 'RED_CARD']),
    color: '#f2c94c',
  },
  { id: 'SUBS', label: 'Subs', kinds: new Set(['SUBSTITUTION']), color: '#c4ccd9' },
  {
    id: 'FOULS',
    label: 'Fouls',
    kinds: new Set(['FOUL', 'OFFSIDE', 'FREE_KICK']),
    color: '#ff9f80',
  },
  { id: 'CORNERS', label: 'Corners', kinds: new Set(['CORNER']), color: '#9b7fff' },
  { id: 'VAR', label: 'VAR', kinds: new Set(['VAR']), color: '#7fb5ff' },
];

// Per-kind colour used for the left border + kind pill. Keep these in
// sync with FILTER_GROUPS so filter chip colour == row colour.
const KIND_COLOR: Record<string, string> = {
  GOAL: '#38d996',
  OWN_GOAL: '#ff7fa3',
  PENALTY_MISSED: '#ef4a56',
  PENALTY_SAVED: '#f2c94c',
  ATTEMPT_SAVED: '#7fb5ff',
  ATTEMPT_BLOCKED: '#9aa6ba',
  ATTEMPT_MISSED: '#6c7891',
  YELLOW_CARD: '#f2c94c',
  RED_CARD: '#ef4a56',
  SUBSTITUTION: '#c4ccd9',
  FOUL: '#ff9f80',
  OFFSIDE: '#ff9f80',
  FREE_KICK: '#9aa6ba',
  CORNER: '#9b7fff',
  VAR: '#7fb5ff',
  PERIOD_START: '#6c7891',
  PERIOD_END: '#6c7891',
  LINEUP: '#6c7891',
  COMMENTARY: '#6c7891',
};

function colorFor(kind: string): string {
  return KIND_COLOR[kind] ?? '#9aa6ba';
}

// Short, human-friendly label for the kind pill on each row. Falls
// back to a title-cased version of the raw kind for anything we
// haven't explicitly mapped (future-proofing when ESPN adds new
// phrases).
const KIND_LABEL: Record<string, string> = {
  GOAL: 'Goal',
  OWN_GOAL: 'Own goal',
  PENALTY_MISSED: 'Pen. miss',
  PENALTY_SAVED: 'Pen. save',
  ATTEMPT_SAVED: 'Shot saved',
  ATTEMPT_BLOCKED: 'Shot blocked',
  ATTEMPT_MISSED: 'Shot off',
  YELLOW_CARD: 'Yellow',
  RED_CARD: 'Red',
  SUBSTITUTION: 'Sub',
  FOUL: 'Foul',
  OFFSIDE: 'Offside',
  FREE_KICK: 'Free kick',
  CORNER: 'Corner',
  VAR: 'VAR',
  PERIOD_START: 'Period',
  PERIOD_END: 'Period',
  LINEUP: 'Lineup',
  COMMENTARY: 'Note',
};

function labelFor(kind: string): string {
  if (KIND_LABEL[kind]) return KIND_LABEL[kind];
  // Fallback: GOAL_SOMETHING_WEIRD -> "Goal something weird"
  const spaced = kind.replace(/_/g, ' ').toLowerCase();
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

// Reverse-chronological ordering helps a reader scan the latest
// action first (same as Flashscore / ESPN gamecast). We sort by
// (period desc, minute desc, addedMinute desc, sequence desc) so
// first-half rows always precede nothing from the second half.
function sortedDesc<T extends MatchCommentary>(rows: ReadonlyArray<T>): T[] {
  return [...rows].sort((a, b) => {
    const pa = a.period ?? 0;
    const pb = b.period ?? 0;
    if (pa !== pb) return pb - pa;
    const ma = a.minute ?? 0;
    const mb = b.minute ?? 0;
    if (ma !== mb) return mb - ma;
    const aa = a.addedMinute ?? 0;
    const ab = b.addedMinute ?? 0;
    if (aa !== ab) return ab - aa;
    return b.sequence - a.sequence;
  });
}

// MatchCommentaryPanel renders a filterable, minute-by-minute feed
// scraped from ESPN. Hidden entirely when no timed commentary rows are
// present (e.g. matches we haven't backfilled yet) so it never shows
// as an empty block.
export function MatchCommentaryPanel({ match }: Props) {
  const rows = useMemo(
    () => (match.commentary ?? []).filter((r): r is TimedCommentary => r.minute != null),
    [match.commentary],
  );
  const [activeFilter, setActiveFilter] = useState<string>('ALL');

  // Per-group counts for the chip badges. Memoised so chip clicks
  // don't re-walk the array on every re-render.
  const counts = useMemo(() => {
    const c: Record<string, number> = { ALL: rows.length };
    for (const group of FILTER_GROUPS) c[group.id] = 0;
    for (const r of rows) {
      for (const group of FILTER_GROUPS) {
        if (group.kinds.has(r.kind)) c[group.id] += 1;
      }
    }
    return c;
  }, [rows]);

  const filtered = useMemo(() => {
    if (activeFilter === 'ALL') return sortedDesc(rows);
    const group = FILTER_GROUPS.find((g) => g.id === activeFilter);
    if (!group) return sortedDesc(rows);
    return sortedDesc(rows.filter((r) => group.kinds.has(r.kind)));
  }, [rows, activeFilter]);

  if (rows.length === 0) return null;

  // Decide which chips to render. We skip any group whose count is 0
  // (no CARDS chip in a cardless match) to keep the strip tight.
  const visibleGroups = FILTER_GROUPS.filter((g) => counts[g.id] > 0);

  return (
    <section className="card" style={{ marginTop: 24 }}>
      <div className="card__header">
        <h2>Minute-by-minute</h2>
        <span className="card__subtitle">
          {rows.length} line{rows.length === 1 ? '' : 's'} · ESPN
        </span>
      </div>

      {/* Filter chip strip */}
      <div
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: 6,
          marginBottom: 12,
        }}
      >
        <FilterChip
          label="All"
          count={counts.ALL}
          active={activeFilter === 'ALL'}
          color="#9aa6ba"
          onClick={() => setActiveFilter('ALL')}
        />
        {visibleGroups.map((g) => (
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

      {/* Feed */}
      <ul
        style={{
          listStyle: 'none',
          margin: 0,
          padding: 0,
          display: 'grid',
          gap: 4,
          maxHeight: 520,
          overflowY: 'auto',
        }}
      >
        {filtered.map((row) => {
          const color = colorFor(row.kind);
          const isSignificant =
            row.kind === 'GOAL' ||
            row.kind === 'OWN_GOAL' ||
            row.kind === 'RED_CARD';
          return (
            <li
              key={row.sequence}
              style={{
                display: 'grid',
                gridTemplateColumns: '50px 90px 1fr 44px',
                gap: 10,
                alignItems: 'baseline',
                padding: '7px 10px',
                borderRadius: 6,
                background: isSignificant ? '#171f2e' : '#121622',
                borderLeft: `3px solid ${color}`,
              }}
            >
              <span
                style={{
                  color: '#9aa6ba',
                  fontSize: 12,
                  fontFeatureSettings: '"tnum"',
                  fontWeight: isSignificant ? 600 : 400,
                }}
              >
                {formatMinute(row.minute, row.addedMinute)}
              </span>
              <span
                style={{
                  color,
                  fontSize: 11,
                  textTransform: 'uppercase',
                  letterSpacing: '0.06em',
                  fontWeight: 600,
                }}
              >
                {labelFor(row.kind)}
              </span>
              <span
                style={{
                  fontSize: 13,
                  color: isSignificant ? '#eaf0f6' : '#c4ccd9',
                  lineHeight: 1.4,
                }}
              >
                {row.text}
              </span>
              <span
                style={{
                  color: '#6c7891',
                  fontSize: 10,
                  textTransform: 'uppercase',
                  letterSpacing: '0.08em',
                  textAlign: 'right',
                }}
              >
                {row.teamSide ?? ''}
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function FilterChip({
  label,
  count,
  active,
  color,
  onClick,
}: {
  label: string;
  count: number;
  active: boolean;
  color: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      style={{
        border: `1px solid ${active ? color : '#2a3245'}`,
        background: active ? `${color}22` : 'transparent',
        color: active ? color : '#9aa6ba',
        borderRadius: 999,
        padding: '4px 10px',
        fontSize: 12,
        cursor: 'pointer',
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        fontWeight: active ? 600 : 400,
        transition: 'background 120ms, color 120ms, border-color 120ms',
      }}
    >
      <span>{label}</span>
      <span
        style={{
          background: active ? `${color}44` : '#1b2131',
          borderRadius: 10,
          padding: '0 6px',
          fontSize: 10,
          color: active ? color : '#6c7891',
          fontFeatureSettings: '"tnum"',
          fontWeight: 600,
        }}
      >
        {count}
      </span>
    </button>
  );
}
