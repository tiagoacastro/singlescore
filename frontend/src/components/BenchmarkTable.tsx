import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { formatPct as pct } from '@/lib/format';

type Props = {
  match: Pick<Match, 'benchmarkPredictions'>;
};

function timeLabel(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

// BenchmarkTable renders the "Benchmark comparison" card: third-party
// forecasts for the match (today: the de-vigged market-average 1X2 odds
// from football-data.co.uk).
export function BenchmarkTable({ match }: Props) {
  const bench = match.benchmarkPredictions ?? [];
  return (
    <div className="card">
      <div className="card__header">
        <h2>Benchmark comparison</h2>
        <span className="card__subtitle">{bench.length} benchmark forecasts</span>
      </div>
      {bench.length ? (
        <table className="data-table">
          <thead>
            <tr>
              <th>Provider</th>
              <th>Home</th>
              <th>Draw</th>
              <th>Away</th>
              <th>Taken at</th>
            </tr>
          </thead>
          <tbody>
            {bench.map((b) => (
              <tr key={b.id}>
                <td>
                  <strong>{b.provider}</strong> <span className="muted">({b.sourceType})</span>
                </td>
                <td className="prob-home">{pct(b.pHome)}</td>
                <td>{pct(b.pDraw)}</td>
                <td className="prob-away">{pct(b.pAway)}</td>
                <td className="muted">{timeLabel(b.forecastAt)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <EmptyState title="No benchmark predictions captured" />
      )}
    </div>
  );
}
