import { useQuery } from '@apollo/client';
import { useParams } from 'react-router-dom';
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
  ResponsiveContainer,
  Legend,
} from 'recharts';
import { MATCH_DETAIL_QUERY } from '@/graphql/queries';
import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { MatchStatusChip, ProvenanceChip } from '@/components/Chip';

function pct(p: number) {
  return `${Math.round(p * 100)}%`;
}

function timeLabel(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
}

export function MatchDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data, loading, error } = useQuery<{ match: Match | null }>(MATCH_DETAIL_QUERY, {
    variables: { id },
    pollInterval: 15000,
    errorPolicy: 'all',
  });

  if (loading && !data) {
    return (
      <section className="card">
        <div className="skeleton skeleton--row" />
        <div className="skeleton skeleton--block" style={{ marginTop: 16 }} />
      </section>
    );
  }

  if (error && !data?.match) {
    return (
      <div className="error-banner">Error loading match: {error.message}</div>
    );
  }

  const m = data?.match;
  if (!m) {
    return <EmptyState title="Match not found" />;
  }

  const chartData =
    m.modelPredictions?.map((p) => ({
      minute: p.matchMinute ?? 0,
      forecastAt: p.forecastAt,
      home: Math.round(p.pHome * 1000) / 10,
      draw: Math.round(p.pDraw * 1000) / 10,
      away: Math.round(p.pAway * 1000) / 10,
    })) ?? [];

  const latest =
    m.modelPredictions && m.modelPredictions.length > 0
      ? m.modelPredictions[m.modelPredictions.length - 1]
      : null;

  return (
    <>
      <header className="page-header">
        <div>
          <div style={{ fontSize: 12, color: '#6c7891', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
            {m.competition.name}
            {m.competition.country ? ` · ${m.competition.country}` : ''}
          </div>
          <h1>
            {m.homeTeam.name} <span style={{ color: '#6c7891' }}>vs</span> {m.awayTeam.name}
          </h1>
        </div>
        <div className="page-header__meta">
          <MatchStatusChip status={m.status} minute={m.minute} />
          <ProvenanceChip provenance={m.provenance} />
        </div>
      </header>

      <section className="grid grid--2up">
        <div className="card">
          <div className="card__header">
            <h2>Score</h2>
            <span className="card__subtitle">kickoff {new Date(m.kickoffAt).toLocaleString()}</span>
          </div>
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: '1fr auto 1fr',
              alignItems: 'center',
              gap: 24,
            }}
          >
            <div style={{ textAlign: 'right', fontSize: 18 }}>{m.homeTeam.name}</div>
            <div style={{ fontSize: 44, fontWeight: 700, fontFeatureSettings: '"tnum"' }}>
              {m.homeScore} <span style={{ opacity: 0.4 }}>·</span> {m.awayScore}
            </div>
            <div style={{ fontSize: 18 }}>{m.awayTeam.name}</div>
          </div>
          {latest ? (
            <div className="prob-triplet">
              <div className="prob prob--home">
                <span className="prob__label">Home win</span>
                <span className="prob__value">{pct(latest.pHome)}</span>
              </div>
              <div className="prob prob--draw">
                <span className="prob__label">Draw</span>
                <span className="prob__value">{pct(latest.pDraw)}</span>
              </div>
              <div className="prob prob--away">
                <span className="prob__label">Away win</span>
                <span className="prob__value">{pct(latest.pAway)}</span>
              </div>
            </div>
          ) : (
            <EmptyState title="No model prediction yet" />
          )}
        </div>

        <div className="card">
          <div className="card__header">
            <h2>Event timeline</h2>
            <span className="card__subtitle">{m.events?.length ?? 0} events</span>
          </div>
          {m.events?.length ? (
            <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 8 }}>
              {m.events.map((e) => (
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
            <EmptyState title="No events recorded" />
          )}
        </div>
      </section>

      <section className="card" style={{ marginTop: 24 }}>
        <div className="card__header">
          <h2>Probability timeline</h2>
          <span className="card__subtitle">
            {m.modelPredictions?.length ?? 0} snapshots
          </span>
        </div>
        {chartData.length > 0 ? (
          <div className="chart-wrap">
            <ResponsiveContainer width="100%" height="100%">
              <LineChart data={chartData} margin={{ top: 10, right: 20, bottom: 20, left: 0 }}>
                <CartesianGrid stroke="#1f2735" strokeDasharray="3 3" />
                <XAxis
                  dataKey="minute"
                  stroke="#6c7891"
                  tickFormatter={(v) => `${v}'`}
                  label={{ value: 'Match minute', position: 'insideBottom', offset: -5, fill: '#6c7891', fontSize: 12 }}
                />
                <YAxis
                  stroke="#6c7891"
                  domain={[0, 100]}
                  tickFormatter={(v) => `${v}%`}
                />
                <Tooltip
                  contentStyle={{ background: '#141924', border: '1px solid #1f2735', borderRadius: 8 }}
                  formatter={(v: number) => `${v.toFixed(1)}%`}
                  labelFormatter={(l, _p) => `Minute ${l}`}
                />
                <Legend wrapperStyle={{ color: '#9aa6ba' }} />
                <Line type="monotone" dataKey="home" stroke="#38d996" dot={false} name="Home" />
                <Line type="monotone" dataKey="draw" stroke="#9aa6ba" dot={false} name="Draw" />
                <Line type="monotone" dataKey="away" stroke="#ff7fa3" dot={false} name="Away" />
              </LineChart>
            </ResponsiveContainer>
          </div>
        ) : (
          <EmptyState title="No prediction snapshots yet" />
        )}
      </section>

      <section className="card" style={{ marginTop: 24 }}>
        <div className="card__header">
          <h2>Benchmark comparison</h2>
          <span className="card__subtitle">
            {m.benchmarkPredictions?.length ?? 0} benchmark forecasts
          </span>
        </div>
        {m.benchmarkPredictions?.length ? (
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
              {m.benchmarkPredictions.map((b) => (
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
      </section>
    </>
  );
}
