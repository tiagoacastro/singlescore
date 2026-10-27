import { useMemo, type CSSProperties } from 'react';
import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { formatMinute } from '@/lib/format';
import { buildTimelineEvents } from '@/lib/timeline';

type Props = {
  match: Pick<
    Match,
    'events' | 'modelPredictions' | 'homeScore' | 'awayScore' | 'homeTeam' | 'awayTeam'
  >;
};

// Icons are plain unicode so they render identically everywhere without
// a webfont round-trip.
const EVENT_STYLE: Record<string, { icon: string; label: string; color: string }> = {
  GOAL: { icon: '⚽', label: 'Goal', color: '#38d996' },
  OWN_GOAL: { icon: '⚽', label: 'Own goal', color: '#ff7fa3' },
  PENALTY_MISSED: { icon: '✕', label: 'Penalty missed', color: '#ef4a56' },
  YELLOW_CARD: { icon: '🟨', label: 'Yellow card', color: '#f2c94c' },
  RED_CARD: { icon: '🟥', label: 'Red card', color: '#ef4a56' },
  SUBSTITUTION: { icon: '↔', label: 'Substitution', color: '#9aa6ba' },
  VAR: { icon: '📺', label: 'VAR', color: '#7fb5ff' },
};
const UNKNOWN_STYLE = { icon: '•', label: 'Event', color: '#9aa6ba' };

// MatchEventList is the match page's "Event timeline" column: one row
// per event with a coloured icon, minute (+ stoppage time), description
// and side. When the per-event feed's goals don't add up to the score,
// goals are reconstructed from the model snapshots (marked "~").
export function MatchEventList({ match }: Props) {
  const rows = useMemo(() => buildTimelineEvents(match), [match]);
  const derived = rows.some((e) => e.approximate);

  return (
    <div className="card">
      <div className="card__header">
        <h2>Event timeline</h2>
        <span className="card__subtitle">
          {rows.length} event{rows.length === 1 ? '' : 's'}
          {derived ? ' · goals derived' : ''}
        </span>
      </div>
      {derived ? (
        <p className="card__note">
          The per-event feed for this match is missing goals, so they're reconstructed from the
          scoreline at each model snapshot: the team is right, the minute (marked <code>~</code>) is
          the first snapshot that shows the goal.
        </p>
      ) : null}
      {rows.length ? (
        <ul className="event-list">
          {rows.map((e) => {
            const style = EVENT_STYLE[e.eventType] ?? UNKNOWN_STYLE;
            return (
              <li
                key={e.id}
                className={`event-row${e.approximate ? ' event-row--approx' : ''}`}
                style={{ '--kind': style.color } as CSSProperties}
                title={`${style.label}${e.teamSide ? ` — ${e.teamSide}` : ''}${
                  e.approximate ? ' (approximate minute)' : ''
                }`}
              >
                <span className="event-row__minute">
                  {formatMinute(e.minute, e.addedMinute, e.approximate) || '-'}
                </span>
                <span className="event-row__icon" aria-hidden>
                  {style.icon}
                </span>
                <span className="event-row__text">
                  <strong>{style.label}</strong>
                  {e.description ? <span> — {e.description}</span> : null}
                </span>
                <span className="event-row__side">{e.teamSide ?? ''}</span>
              </li>
            );
          })}
        </ul>
      ) : (
        <EmptyState title="No per-event data" />
      )}
    </div>
  );
}
