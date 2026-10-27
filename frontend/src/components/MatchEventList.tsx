import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';

type Props = {
  match: Pick<Match, 'events' | 'provenance'>;
};

// Visual metadata per event type. Icons are deliberately plain unicode
// so they render identically on every OS without a webfont round-trip.
const EVENT_STYLE: Record<string, { icon: string; label: string; color: string }> = {
  GOAL:         { icon: '⚽', label: 'Goal',           color: '#38d996' },
  OWN_GOAL:     { icon: '⚽', label: 'Own goal',       color: '#ff7fa3' },
  PENALTY:      { icon: '🎯', label: 'Penalty',        color: '#38d996' },
  YELLOW_CARD:  { icon: '🟨', label: 'Yellow card',    color: '#f2c94c' },
  RED_CARD:     { icon: '🟥', label: 'Red card',       color: '#ef4a56' },
  SUBSTITUTION: { icon: '↔',  label: 'Substitution',   color: '#9aa6ba' },
  VAR:          { icon: '📺', label: 'VAR',            color: '#7fb5ff' },
};
const UNKNOWN_STYLE = { icon: '•', label: 'Event', color: '#9aa6ba' };

function styleFor(eventType: string) {
  return EVENT_STYLE[eventType] ?? UNKNOWN_STYLE;
}

// MatchEventList renders the right-hand "Event timeline" panel on the
// match detail page. Flashscore-style running feed: one row per event
// with a type-coloured icon, minute (+ stoppage time), description and
// the team side. Empty state explains the free-tier 3-day window
// limitation (api-football) when there's no event data for an older
// match.
export function MatchEventList({ match }: Props) {
  const events = match.events ?? [];
  return (
    <div className="card">
      <div className="card__header">
        <h2>Event timeline</h2>
        <span className="card__subtitle">{events.length} events</span>
      </div>
      {events.length ? (
        <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 6 }}>
          {events.map((e) => {
            const style = styleFor(e.eventType);
            return (
              <li
                key={e.id}
                style={{
                  display: 'grid',
                  gridTemplateColumns: '52px 24px 1fr auto',
                  gap: 10,
                  alignItems: 'center',
                  padding: '8px 10px',
                  borderRadius: 8,
                  background: '#141924',
                  borderLeft: `3px solid ${style.color}`,
                }}
                title={`${e.eventType} — ${e.teamSide ?? ''}`}
              >
                <span style={{ color: '#9aa6ba', fontFeatureSettings: '"tnum"', fontSize: 13 }}>
                  {e.minute ?? '-'}'{e.addedMinute ? `+${e.addedMinute}` : ''}
                </span>
                <span style={{ fontSize: 16, textAlign: 'center' }} aria-hidden>
                  {style.icon}
                </span>
                <span style={{ fontSize: 14 }}>
                  <strong style={{ color: style.color }}>{style.label}</strong>
                  {e.description ? (
                    <span style={{ color: '#c4ccd9' }}> — {e.description}</span>
                  ) : null}
                </span>
                <span style={{ color: '#6c7891', fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                  {e.teamSide ?? ''}
                </span>
              </li>
            );
          })}
        </ul>
      ) : (
        <EmptyState
          title="No events recorded"
          description={
            match.provenance === 'LIVE'
              ? 'Per-event data (goals, cards, subs, VAR) is ingested from api-football.com, whose free tier only covers the last 24h / today / tomorrow. Older matches have no event timeline.'
              : undefined
          }
        />
      )}
    </div>
  );
}
