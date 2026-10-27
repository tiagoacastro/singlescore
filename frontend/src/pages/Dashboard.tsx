import { useQuery } from '@apollo/client';
import { DASHBOARD_QUERY } from '@/graphql/queries';
import type { Dashboard } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { MatchRow } from '@/components/MatchRow';
import { SkeletonRows } from '@/components/SkeletonRows';
import { formatNumber, formatPct, formatRelative } from '@/lib/format';

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
              {formatNumber(dash?.modelMetrics?.brierScore)}
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
              {formatNumber(dash?.modelMetrics?.logLoss)}
            </span>
            <span className="metric__meta">
              accuracy {dash?.modelMetrics ? formatPct(dash.modelMetrics.accuracy) : '—'}
            </span>
          </div>
        </div>
        <div className="card">
          <div className="card__header">
            <h3>System</h3>
            <span className="card__subtitle">status</span>
          </div>
          <div className="metric">
            <span className="metric__value metric__value--sm">
              db:{' '}
              <strong className={dash?.systemStatus?.databaseReachable ? 'text-ok' : 'text-bad'}>
                {dash?.systemStatus?.databaseReachable ? 'reachable' : 'unreachable'}
              </strong>
            </span>
            <span className="metric__meta">
              build v{dash?.systemStatus?.version ?? '—'} · started{' '}
              {formatRelative(dash?.systemStatus?.startedAt)}
            </span>
          </div>
        </div>
      </section>

      <section className="grid grid--2up section-gap" aria-label="Recent matches">
        <div className="card">
          <div className="card__header">
            <h2>Recent finished matches</h2>
            <span className="card__subtitle">pre-match odds</span>
          </div>
          {loading && !dash ? (
            <SkeletonRows />
          ) : dash?.recentFinishedMatches.length ? (
            <div className="stack stack--tight">
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
            <ul className="source-list">
              {dash.dataSources.map((ds) => (
                <li key={`${ds.provider}-${ds.jobName}`} className="source-list__item">
                  <div>
                    <strong>{ds.provider}</strong>
                    <span className="source-list__meta">{ds.jobName}</span>
                  </div>
                  <div className="source-list__run">
                    <div className={ds.lastStatus === 'SUCCEEDED' ? undefined : 'text-bad'}>
                      {ds.lastStatus ?? '—'}
                    </div>
                    <span className="source-list__meta">
                      {formatRelative(ds.lastRunAt)} · in {ds.recordsIn} / out {ds.recordsOut}
                    </span>
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
