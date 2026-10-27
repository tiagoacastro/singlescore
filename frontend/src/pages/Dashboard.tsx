import { useQuery } from '@apollo/client';
import { DASHBOARD_QUERY } from '@/graphql/queries';
import type { Dashboard } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { MatchRow } from '@/components/MatchRow';

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
    errorPolicy: 'all',
  });
  const dash = data?.dashboard;

  return (
    <>
      <header className="page-header">
        <h1>Overview</h1>
        <div className="page-header__meta">
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
            <span className="card__subtitle">pre-match</span>
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
            <span className="card__subtitle">pre-match</span>
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
            <span className="card__subtitle">status</span>
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

      <section className="grid grid--2up" style={{ marginTop: 24 }} aria-label="Recent matches">
        <div className="card">
          <div className="card__header">
            <h2>Recent finished matches</h2>
            <span className="card__subtitle">pre-match odds</span>
          </div>
          {loading && !dash ? (
            <>
              <div className="skeleton skeleton--row" />
              <div className="skeleton skeleton--row" />
              <div className="skeleton skeleton--row" />
            </>
          ) : dash?.recentFinishedMatches.length ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {dash.recentFinishedMatches.map((m) => (
                <MatchRow key={m.id} match={m} showProbabilities />
              ))}
            </div>
          ) : (
            <EmptyState title="No completed matches yet" />
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
              description="Run the hourly ingest notebooks to populate this list."
            />
          )}
        </div>
      </section>
    </>
  );
}
