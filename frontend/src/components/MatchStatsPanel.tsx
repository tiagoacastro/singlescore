import type { Match } from '@/graphql/types';

type Props = {
  match: Pick<Match, 'stats' | 'homeTeam' | 'awayTeam'>;
};

type StatRowProps = {
  label: string;
  home: number | null | undefined;
  away: number | null | undefined;
  format?: (v: number) => string;
};

// One home-vs-away row: both values, with the side that had more
// highlighted, over a bar split by each side's share. Rows where both
// values are missing are skipped (no fabricated zeros).
function StatRow({ label, home, away, format = String }: StatRowProps) {
  if (home == null && away == null) return null;
  const h = home ?? 0;
  const a = away ?? 0;
  // Both zero: show an even split rather than dividing by zero.
  const homePct = h + a > 0 ? (h / (h + a)) * 100 : 50;
  const lead = h > a ? 'home' : a > h ? 'away' : null;
  return (
    <div className={`stat-row${lead ? ` stat-row--${lead}` : ''}`}>
      <div className="stat-row__values">
        <span className="stat-row__home">{home == null ? '—' : format(home)}</span>
        <span className="stat-row__label">{label}</span>
        <span className="stat-row__away">{away == null ? '—' : format(away)}</span>
      </div>
      <div
        className="stat-row__bar"
        style={{ gridTemplateColumns: `${homePct.toFixed(2)}% ${(100 - homePct).toFixed(2)}%` }}
        aria-hidden
      >
        <div />
        <div />
      </div>
    </div>
  );
}

// Aggregate match stats from football-data.co.uk, ordered roughly like
// Flashscore / SofaScore: xG, shots, set pieces, discipline. Hidden when
// the match has no stats.
export function MatchStatsPanel({ match }: Props) {
  const s = match.stats;
  if (!s) return null;

  const rows: StatRowProps[] = [
    { label: 'Expected goals', home: s.homeXg, away: s.awayXg, format: (v) => v.toFixed(2) },
    { label: 'Shots', home: s.homeShots, away: s.awayShots },
    { label: 'Shots on target', home: s.homeShotsOnTarget, away: s.awayShotsOnTarget },
    { label: 'Corners', home: s.homeCorners, away: s.awayCorners },
    { label: 'Fouls', home: s.homeFouls, away: s.awayFouls },
    { label: 'Yellow cards', home: s.homeYellowCards, away: s.awayYellowCards },
    { label: 'Red cards', home: s.homeRedCards, away: s.awayRedCards },
  ];
  if (!rows.some((r) => r.home != null || r.away != null) && !s.referee) return null;

  return (
    <section className="card">
      <div className="card__header">
        <h2>Match stats</h2>
        <span className="card__subtitle">
          {match.homeTeam.name} vs {match.awayTeam.name}
        </span>
      </div>
      <div className="stack stack--compact">
        {rows.map((r) => (
          <StatRow key={r.label} {...r} />
        ))}
      </div>
      <div className="stats-footer">
        <span>
          {s.referee ? (
            <>
              Referee: <strong>{s.referee}</strong>
            </>
          ) : null}
        </span>
        <span className="stats-footer__source">source: {s.source}</span>
      </div>
    </section>
  );
}
