import { useMemo } from 'react';
import type { Match, MatchEvent } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';

type Props = {
  match: Pick<
    Match,
    'events' | 'provenance' | 'modelPredictions' | 'homeTeam' | 'awayTeam'
  >;
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

// A superset of MatchEvent used for rendering. Real events come from
// api-football; derived ones are synthesised client-side from the
// scoreline-at-forecast deltas on model predictions when api-football
// has no feed for the match (older than 3 days on the free tier).
type RowEvent = Pick<MatchEvent, 'id' | 'eventType' | 'teamSide' | 'description'> & {
  minute: number | null;
  addedMinute?: number | null;
  approximate?: boolean;
};

// MatchEventList renders the right-hand "Event timeline" panel on the
// match detail page. Flashscore-style running feed: one row per event
// with a type-coloured icon, minute (+ stoppage time), description and
// the team side. If per-event data is missing we fall back to a goal
// list derived from the stored scoreline-at-each-snapshot so the panel
// is never empty for a finished match that actually had goals.
export function MatchEventList({ match }: Props) {
  const realEvents = match.events ?? [];

  // Derive goal rows from scoreline deltas across the latest model's
  // snapshots. Same strategy ProbabilityTimeline uses for its markers;
  // we duplicate it here (rather than lift it to a shared hook) so
  // this component stays independently self-contained.
  const derived = useMemo<RowEvent[]>(() => {
    if (realEvents.length > 0) return [];
    const preds = match.modelPredictions ?? [];
    if (preds.length === 0) return [];
    // Pick the newest (name, version) pair by forecastAt so a stale
    // older model's rows don't double-count goals.
    let latestKey = '';
    let latestForecast = -Infinity;
    for (const p of preds) {
      const t = Date.parse(p.forecastAt);
      if (Number.isFinite(t) && t > latestForecast) {
        latestForecast = t;
        latestKey = `${p.modelName}@${p.modelVersion}`;
      }
    }
    const sorted = preds
      .filter((p) => `${p.modelName}@${p.modelVersion}` === latestKey)
      .slice()
      .sort((a, b) => {
        const ma = a.matchMinute ?? 0;
        const mb = b.matchMinute ?? 0;
        if (ma !== mb) return ma - mb;
        return Date.parse(a.forecastAt) - Date.parse(b.forecastAt);
      });
    const rows: RowEvent[] = [];
    let prevH = 0;
    let prevA = 0;
    sorted.forEach((p, i) => {
      const h = p.homeScoreAtForecast ?? 0;
      const a = p.awayScoreAtForecast ?? 0;
      if (i === 0) {
        prevH = h;
        prevA = a;
        return;
      }
      const dH = h - prevH;
      const dA = a - prevA;
      const minute = p.matchMinute ?? 0;
      for (let g = 0; g < dH; g++) {
        rows.push({
          id: `derived-H-${minute}-${g}`,
          eventType: 'GOAL',
          teamSide: 'HOME',
          description: `${match.homeTeam.name} — exact minute unknown`,
          minute,
          approximate: true,
        });
      }
      for (let g = 0; g < dA; g++) {
        rows.push({
          id: `derived-A-${minute}-${g}`,
          eventType: 'GOAL',
          teamSide: 'AWAY',
          description: `${match.awayTeam.name} — exact minute unknown`,
          minute,
          approximate: true,
        });
      }
      prevH = h;
      prevA = a;
    });
    return rows;
  }, [realEvents.length, match.modelPredictions, match.homeTeam.name, match.awayTeam.name]);

  const rows: RowEvent[] = realEvents.length
    ? realEvents.map((e) => ({
        id: e.id,
        eventType: e.eventType,
        teamSide: e.teamSide,
        description: e.description,
        minute: e.minute ?? null,
        addedMinute: e.addedMinute,
        approximate: false,
      }))
    : derived;

  const headerCount = rows.length;
  const isDerived = realEvents.length === 0 && derived.length > 0;

  return (
    <div className="card">
      <div className="card__header">
        <h2>Event timeline</h2>
        <span className="card__subtitle">
          {headerCount} event{headerCount === 1 ? '' : 's'}
          {isDerived ? ' · derived' : ''}
        </span>
      </div>
      {rows.length ? (
        <>
          {isDerived ? (
            <p
              style={{
                fontSize: 12,
                color: '#9aa6ba',
                margin: '0 0 10px',
                lineHeight: 1.4,
              }}
            >
              api-football has no per-event feed for this match (free tier
              only covers the last 24h / today / tomorrow). The goals below
              are reconstructed from the HT and FT scoreline — teams are
              correct, minutes are approximate (marked with <code>~</code>).
            </p>
          ) : null}
          <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 6 }}>
            {rows.map((e) => {
              const style = styleFor(e.eventType);
              const minuteLabel =
                e.minute == null
                  ? '-'
                  : e.approximate
                    ? `~${e.minute}'`
                    : `${e.minute}'${e.addedMinute ? `+${e.addedMinute}` : ''}`;
              return (
                <li
                  key={e.id}
                  style={{
                    display: 'grid',
                    gridTemplateColumns: '58px 24px 1fr auto',
                    gap: 10,
                    alignItems: 'center',
                    padding: '8px 10px',
                    borderRadius: 8,
                    background: '#141924',
                    borderLeft: `3px solid ${style.color}`,
                    borderLeftStyle: e.approximate ? 'dashed' : 'solid',
                  }}
                  title={`${e.eventType} — ${e.teamSide ?? ''}${
                    e.approximate ? ' (approximate minute)' : ''
                  }`}
                >
                  <span
                    style={{
                      color: e.approximate ? '#6c7891' : '#9aa6ba',
                      fontStyle: e.approximate ? 'italic' : 'normal',
                      fontFeatureSettings: '"tnum"',
                      fontSize: 13,
                    }}
                  >
                    {minuteLabel}
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
                  <span
                    style={{
                      color: '#6c7891',
                      fontSize: 11,
                      textTransform: 'uppercase',
                      letterSpacing: '0.08em',
                    }}
                  >
                    {e.teamSide ?? ''}
                  </span>
                </li>
              );
            })}
          </ul>
        </>
      ) : (
        <EmptyState
          title="No per-event data"
          description={
            match.provenance === 'LIVE'
              ? "Per-event data (cards, subs, VAR) is ingested from api-football.com, whose free tier only covers the last 24h / today / tomorrow. Older matches with no goals have no event feed."
              : undefined
          }
        />
      )}
    </div>
  );
}
