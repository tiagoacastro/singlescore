import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';

type Props = {
  match: Pick<Match, 'events' | 'provenance'>;
};

// MatchEventList renders the right-hand "Event timeline" panel on the
// match detail page. One row per event (goals, red cards, etc.) with
// minute + description. Empty state explains the free-tier 3-day
// window limitation when there's no event data for an older match.
export function MatchEventList({ match }: Props) {
  const events = match.events ?? [];
  return (
    <div className="card">
      <div className="card__header">
        <h2>Event timeline</h2>
        <span className="card__subtitle">{events.length} events</span>
      </div>
      {events.length ? (
        <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 8 }}>
          {events.map((e) => (
            <li
              key={e.id}
              style={{
                display: 'grid',
                gridTemplateColumns: '48px 1fr auto',
                gap: 10,
                alignItems: 'center',
                padding: '8px 10px',
                borderRadius: 8,
                background: '#141924',
              }}
            >
              <span style={{ color: '#9aa6ba', fontFeatureSettings: '"tnum"' }}>
                {e.minute ?? '-'}'{e.addedMinute ? `+${e.addedMinute}` : ''}
              </span>
              <span>
                <strong>{e.eventType}</strong>
                {e.description ? ` — ${e.description}` : ''}
              </span>
              <span style={{ color: '#6c7891', fontSize: 12 }}>{e.teamSide ?? ''}</span>
            </li>
          ))}
        </ul>
      ) : (
        <EmptyState
          title="No events recorded"
          description={
            match.provenance === 'LIVE'
              ? 'Per-event data (goals, cards) is ingested from api-football.com, whose free tier only covers the last 24h / today / tomorrow. Older matches have no event timeline.'
              : undefined
          }
        />
      )}
    </div>
  );
}
