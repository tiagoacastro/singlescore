import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';

type Props = {
  match: Pick<Match, 'benchmarkPredictions' | 'provenance'>;
};

function pct(p: number) {
  return `${Math.round(p * 100)}%`;
}

function timeLabel(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

// BenchmarkTable renders the "Benchmark comparison" card: a table of
// third-party predictions (sportsbook odds, other models) alongside
// our own forecast. Empty state explains that football-data.org does
// not expose odds, so a market-odds adapter needs wiring to populate.
export function BenchmarkTable({ match }: Props) {
  const bench = match.benchmarkPredictions ?? [];
  return (
    <div className="card" style={{ marginTop: 24 }}>
      <div className="card__header">
        <h2>Benchmark comparison</h2>
        <span className="card__subtitle">{bench.length} benchmark forecasts</span>
      </div>
      {bench.length ? (
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ color: '#6c7891', fontSize: 12, textAlign: 'left', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
              <th style={{ padding: '8px 10px' }}>Provider</th>
              <th>Horizon</th>
              <th>Minute</th>
              <th>Home</th>
              <th>Draw</th>
              <th>Away</th>
              <th>Taken at</th>
            </tr>
          </thead>
          <tbody>
            {bench.map((b) => (
              <tr key={b.id} style={{ borderTop: '1px solid #1f2735', fontFeatureSettings: '"tnum"' }}>
                <td style={{ padding: '8px 10px' }}>
                  <strong>{b.provider}</strong>{' '}
                  <span style={{ color: '#6c7891', fontSize: 12 }}>({b.sourceType})</span>
                </td>
                <td>{b.horizon}</td>
                <td>{b.matchMinute ?? '-'}</td>
                <td style={{ color: '#38d996' }}>{pct(b.pHome)}</td>
                <td>{pct(b.pDraw)}</td>
                <td style={{ color: '#ff7fa3' }}>{pct(b.pAway)}</td>
                <td style={{ color: '#9aa6ba' }}>{timeLabel(b.forecastAt)}</td>
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
