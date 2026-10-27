import { useQuery } from '@apollo/client';
import { DASHBOARD_QUERY } from '@/graphql/queries';
import type { Dashboard } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { MatchRow } from '@/components/MatchRow';
import { ProvenanceChip } from '@/components/Chip';

function fmtNumber(n: number | undefined | null, digits = 3) {
  if (n === undefined || n === null || Number.isNaN(n)) return '—';
  return n.toFixed(digits);
}

function fmtRelative(iso: string | null | undefined): string {
  if (!iso) return 'never';
  const diffMs = Date.now() - new Date(iso).getTime();
  const diffSec = Math.round(diffMs / 1000);
  if (diffSec < 60) return `${diffSec}s ago`;
  const diffMin = Math.round(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.round(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  return new Date(iso).toLocaleDateString();
}

export function DashboardPage() {
  const { data, loading, error } = useQuery<{ dashboard: Dashboard }>(DASHBOARD_QUERY, {
    pollInterval: 15000,
    errorPolicy: 'all',
  });
  const dash = data?.dashboard;

  return (
    <>
      <header className="page-header">
        <h1>Overview</h1>
        <div className="page-header__meta">
          {dash?.systemStatus?.demoMode ? <ProvenanceChip provenance="DEMO" /> : null}
          <span className="chip">{dash?.systemStatus?.status ?? '…'}</span>
        </div>
      </header>

      {error ? (
        <div className="error-banner">
          GraphQL error: {error.message}. Showing cached data where available.
        </div>
      ) : null}

      <section className="grid grid--3up" aria-label="Model summary">
        <div className="card">
          <div className="card__header">
            <h3>Model Brier score</h3>
            <span className="card__subtitle">lower is better</span>
          </div>
          <div className="metric">
            <span className="metric__value">
              {fmtNumber(dash?.modelMetrics?.brierScore)}
            </span>
            <span className="metric__meta">
              n = {dash?.modelMetrics?.sampleSize ?? 0}
              {dash?.modelMetrics
                ? ` · ${dash.modelMetrics.modelName} ${dash.modelMetrics.modelVersion}`
                : ''}
            </span>
          </div>
        </div>
        <div className="card">
          <div className="card__header">
            <h3>Model log loss</h3>
            <span className="card__subtitle">lower is better</span>
          </div>
          <div className="metric">
            <span className="metric__value">
              {fmtNumber(dash?.modelMetrics?.logLoss)}
            </span>
            <span className="metric__meta">
              accuracy {dash?.modelMetrics ? Math.round(dash.modelMetrics.accuracy * 100) + '%' : '—'}
            </span>
          </div>
        </div>
        <div className="card">
          <div className="card__header">
            <h3>System</h3>
            <span className="card__subtitle">live status</span>
          </div>
          <div className="metric">
            <span className="metric__value" style={{ fontSize: 20 }}>
              db:{' '}
              <strong
                style={{
                  color: dash?.systemStatus?.databaseReachable ? '#38d996' : '#ff5a68',
                }}
              >
                {dash?.systemStatus?.databaseReachable ? 'reachable' : 'unreachable'}
              </strong>
            </span>
            <span className="metric__meta">
              build v{dash?.systemStatus?.version ?? '—'} · started{' '}
              {fmtRelative(dash?.systemStatus?.startedAt)}
            </span>
          </div>
        </div>
      </section>

      <section className="grid grid--2up" style={{ marginTop: 24 }} aria-label="Live and recent">
        <div className="card">
          <div className="card__header">
            <h2>Live matches</h2>
            <span className="card__subtitle">
              {dash?.liveMatches.length ?? 0} tracked
            </span>
          </div>
          {loading && !dash ? (
            <>
              <div className="skeleton skeleton--row" />
              <div className="skeleton skeleton--row" />
              <div className="skeleton skeleton--row" />
            </>
          ) : dash?.liveMatches.length ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {dash.liveMatches.map((m) => (
                <MatchRow key={m.id} match={m} showProbabilities />
              ))}
            </div>
          ) : (
            <EmptyState
              title="No live matches right now"
              description="Live matches will appear here while they're in progress."
            />
          )}
        </div>

        <div className="card">
          <div className="card__header">
            <h2>Data sources</h2>
            <span className="card__subtitle">last run</span>
          </div>
          {dash?.dataSources?.length ? (
            <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 10 }}>
              {dash.dataSources.map((ds) => (
                <li
                  key={`${ds.provider}-${ds.jobName}`}
                  style={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    gap: 12,
                    padding: '10px 12px',
                    background: '#141924',
                    borderRadius: 8,
                  }}
                >
                  <div style={{ display: 'flex', flexDirection: 'column' }}>
                    <strong>{ds.provider}</strong>
                    <span style={{ fontSize: 12, color: '#9aa6ba' }}>{ds.jobName}</span>
                  </div>
                  <div style={{ textAlign: 'right' }}>
                    <div>{ds.lastStatus ?? '—'}</div>
                    <div style={{ fontSize: 12, color: '#9aa6ba' }}>
                      {fmtRelative(ds.lastRunAt)} · in {ds.recordsIn} / out {ds.recordsOut}
                    </div>
                  </div>
                </li>
              ))}
            </ul>
          ) : (
            <EmptyState
              title="No ingestion runs yet"
              description="Run the demo seeder or configure a provider to populate this list."
            />
          )}
        </div>
      </section>

      <section className="card" style={{ marginTop: 24 }} aria-label="Recent finished matches">
        <div className="card__header">
          <h2>Recent finished matches</h2>
          <span className="card__subtitle">
            {dash?.recentFinishedMatches.length ?? 0}
          </span>
        </div>
        {dash?.recentFinishedMatches.length ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {dash.recentFinishedMatches.map((m) => (
              <MatchRow key={m.id} match={m} />
            ))}
          </div>
        ) : (
          <EmptyState title="No completed matches yet" />
        )}
      </section>

      <section className="card" style={{ marginTop: 24 }} aria-label="Recent prediction changes">
        <div className="card__header">
          <h2>Recent prediction changes</h2>
          <span className="card__subtitle">largest moves in the last snapshot</span>
        </div>
        {dash?.recentPredictionChanges?.length ? (
          <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 8 }}>
            {dash.recentPredictionChanges.map((pc, i) => (
              <li
                key={pc.prediction.id ?? i}
                style={{
                  display: 'grid',
                  gridTemplateColumns: '1.4fr auto 1.4fr',
                  gap: 12,
                  alignItems: 'center',
                  padding: '10px 12px',
                  borderRadius: 8,
                  background: '#141924',
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <strong>{pc.match.homeTeam.shortName ?? pc.match.homeTeam.name}</strong>
                  <span style={{ color: '#6c7891' }}>vs</span>
                  <strong>{pc.match.awayTeam.shortName ?? pc.match.awayTeam.name}</strong>
                </div>
                <div style={{ fontSize: 12, color: '#9aa6ba' }}>
                  {pc.match.status.toLowerCase()}{' '}
                  {pc.match.minute != null ? `${pc.match.minute}'` : ''}
                </div>
                <div
                  style={{
                    display: 'flex',
                    justifyContent: 'flex-end',
                    gap: 10,
                    fontFeatureSettings: '"tnum"',
                  }}
                >
                  <span style={{ color: '#38d996' }}>H {signed(pc.deltaHome)}</span>
                  <span style={{ color: '#9aa6ba' }}>D {signed(pc.deltaDraw)}</span>
                  <span style={{ color: '#ff7fa3' }}>A {signed(pc.deltaAway)}</span>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <EmptyState title="No prediction snapshots yet" />
        )}
      </section>
    </>
  );
}

function signed(x: number): string {
  const n = Math.round(x * 100);
  return `${n > 0 ? '+' : ''}${n}%`;
}
