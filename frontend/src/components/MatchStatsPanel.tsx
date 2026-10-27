import type { Match, MatchStats } from '@/graphql/types';

type Props = {
  match: Pick<Match, 'stats' | 'homeTeam' | 'awayTeam'>;
};

// A single "home-vs-away" tile. Shows the two numbers side-by-side
// with a dual-direction bar underneath proportional to each side's
// share of the total. If either value is null the whole row is
// skipped (we don't fabricate zeros for missing data).
function StatRow({
  label,
  home,
  away,
  format = (v) => String(v),
}: {
  label: string;
  home: number | null | undefined;
  away: number | null | undefined;
  format?: (v: number) => string;
}) {
  if (home == null && away == null) return null;
  const h = home ?? 0;
  const a = away ?? 0;
  const total = h + a;
  // Guard against both-zero (avoid NaN); fall back to a 50/50 split
  // which visually conveys "neither side had any of this".
  const homePct = total > 0 ? (h / total) * 100 : 50;
  const awayPct = 100 - homePct;
  // Simple threshold: side with the higher raw count is highlighted
  // so the viewer can scan a column of rows and immediately see who
  // dominated each metric. Draws keep both muted.
  const homeLead = h > a;
  const awayLead = a > h;
  return (
    <div style={{ display: 'grid', gap: 4 }}>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: '1fr auto 1fr',
          alignItems: 'baseline',
          gap: 8,
          fontSize: 13,
        }}
      >
        <span
          style={{
            textAlign: 'right',
            color: homeLead ? '#38d996' : '#c4ccd9',
            fontWeight: homeLead ? 600 : 400,
            fontFeatureSettings: '"tnum"',
          }}
        >
          {home == null ? '—' : format(home)}
        </span>
        <span
          style={{
            color: '#6c7891',
            textTransform: 'uppercase',
            letterSpacing: '0.06em',
            fontSize: 11,
          }}
        >
          {label}
        </span>
        <span
          style={{
            textAlign: 'left',
            color: awayLead ? '#ff7fa3' : '#c4ccd9',
            fontWeight: awayLead ? 600 : 400,
            fontFeatureSettings: '"tnum"',
          }}
        >
          {away == null ? '—' : format(away)}
        </span>
      </div>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: `${homePct.toFixed(2)}% ${awayPct.toFixed(2)}%`,
          height: 4,
          borderRadius: 2,
          overflow: 'hidden',
          background: '#1a2030',
        }}
        aria-hidden
      >
        <div
          style={{
            background: homeLead ? '#38d996' : '#2a3344',
            height: '100%',
            transition: 'width 240ms ease',
          }}
        />
        <div
          style={{
            background: awayLead ? '#ff7fa3' : '#2a3344',
            height: '100%',
            transition: 'width 240ms ease',
          }}
        />
      </div>
    </div>
  );
}

// Flashscore-style match-stats panel. Shows the aggregate counts we
// ingest from football-data.co.uk — one row per metric with the raw
// values + a dual-direction proportional bar. The whole panel is
// hidden when the match has no stats row at all.
export function MatchStatsPanel({ match }: Props) {
  const s: MatchStats | null | undefined = match.stats;
  if (!s) return null;

  // Row definitions. Order chosen to roughly mirror what Flashscore
  // and SofaScore show above-the-fold: xG → shots → shots on target →
  // possession-adjacent (corners, fouls) → discipline (cards).
  const rows: Array<{
    label: string;
    home: number | null | undefined;
    away: number | null | undefined;
    format?: (v: number) => string;
  }> = [
    { label: 'Expected goals', home: s.homeXg, away: s.awayXg, format: (v) => v.toFixed(2) },
    { label: 'Shots', home: s.homeShots, away: s.awayShots },
    { label: 'Shots on target', home: s.homeShotsOnTarget, away: s.awayShotsOnTarget },
    { label: 'Corners', home: s.homeCorners, away: s.awayCorners },
    { label: 'Fouls', home: s.homeFouls, away: s.awayFouls },
    { label: 'Yellow cards', home: s.homeYellowCards, away: s.awayYellowCards },
    { label: 'Red cards', home: s.homeRedCards, away: s.awayRedCards },
  ];

  const anyPopulated = rows.some((r) => r.home != null || r.away != null);
  if (!anyPopulated && !s.referee) return null;

  return (
    <section className="card" style={{ marginTop: 24 }}>
      <div className="card__header">
        <h2>Match stats</h2>
        <span className="card__subtitle">
          {match.homeTeam.name} <span style={{ color: '#6c7891' }}>vs</span>{' '}
          {match.awayTeam.name}
        </span>
      </div>
      <div style={{ display: 'grid', gap: 12 }}>
        {rows.map((r) => (
          <StatRow key={r.label} {...r} />
        ))}
      </div>
      {s.referee ? (
        <div
          style={{
            marginTop: 16,
            paddingTop: 12,
            borderTop: '1px solid #1f2635',
            fontSize: 12,
            color: '#9aa6ba',
            display: 'flex',
            justifyContent: 'space-between',
          }}
        >
          <span>
            Referee: <strong style={{ color: '#c4ccd9' }}>{s.referee}</strong>
          </span>
          <span style={{ color: '#6c7891', textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            source: {s.source}
          </span>
        </div>
      ) : null}
    </section>
  );
}
