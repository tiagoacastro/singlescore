import { useQuery } from '@apollo/client';
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
  ResponsiveContainer,
  Legend,
  ReferenceLine,
} from 'recharts';
import { MODEL_EVALUATION_QUERY } from '@/graphql/queries';
import type { EvaluationReport } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';

function pct(x: number) {
  return `${(x * 100).toFixed(1)}%`;
}

export function EvaluationPage() {
  const { data, loading, error } = useQuery<{ modelEvaluation: EvaluationReport }>(
    MODEL_EVALUATION_QUERY,
    { errorPolicy: 'all' },
  );

  const report = data?.modelEvaluation;
  const summary = report?.summary ?? null;

  return (
    <>
      <header className="page-header">
        <h1>Model evaluation</h1>
      </header>

      {error ? <div className="error-banner">GraphQL error: {error.message}</div> : null}

      <section className="grid grid--3up">
        <div className="card">
          <div className="card__header">
            <h3>Brier score</h3>
            <span className="card__subtitle">lower is better</span>
          </div>
          <div className="metric">
            <span className="metric__value">
              {summary ? summary.brierScore.toFixed(3) : '—'}
            </span>
            <span className="metric__meta">
              n = {summary?.sampleSize ?? 0}{' '}
              {summary ? ` · ${summary.modelName} ${summary.modelVersion}` : ''}
            </span>
          </div>
        </div>
        <div className="card">
          <div className="card__header">
            <h3>Log loss</h3>
            <span className="card__subtitle">lower is better</span>
          </div>
          <div className="metric">
            <span className="metric__value">
              {summary ? summary.logLoss.toFixed(3) : '—'}
            </span>
            <span className="metric__meta">
              {summary ? `uniform baseline ≈ ${(-Math.log(1 / 3)).toFixed(3)}` : ''}
            </span>
          </div>
        </div>
        <div className="card">
          <div className="card__header">
            <h3>Accuracy</h3>
            <span className="card__subtitle">argmax == actual</span>
          </div>
          <div className="metric">
            <span className="metric__value">
              {summary ? pct(summary.accuracy) : '—'}
            </span>
            <span className="metric__meta">
              date range: {summary?.dateRangeFrom?.substring(0, 10) ?? '—'} →{' '}
              {summary?.dateRangeTo?.substring(0, 10) ?? '—'}
            </span>
          </div>
        </div>
      </section>

      <section className="card" style={{ marginTop: 24 }}>
        <div className="card__header">
          <h2>Calibration</h2>
          <span className="card__subtitle">predicted vs observed frequency</span>
        </div>
        {loading && !report ? (
          <div className="skeleton skeleton--block" />
        ) : report?.calibration?.some((b) => b.sampleSize > 0) ? (
          <div className="chart-wrap">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart
                data={report.calibration.map((b) => ({
                  bin: `${Math.round(b.binLow * 100)}–${Math.round(b.binHigh * 100)}%`,
                  predicted: Math.round(b.avgPredicted * 100),
                  observed: Math.round(b.observedFrequency * 100),
                  n: b.sampleSize,
                }))}
                margin={{ top: 10, right: 20, bottom: 20, left: 0 }}
              >
                <CartesianGrid stroke="#1f2735" strokeDasharray="3 3" />
                <XAxis dataKey="bin" stroke="#6c7891" />
                <YAxis stroke="#6c7891" tickFormatter={(v) => `${v}%`} domain={[0, 100]} />
                <Tooltip
                  contentStyle={{ background: '#141924', border: '1px solid #1f2735', borderRadius: 8 }}
                  formatter={(v: number) => `${v}%`}
                />
                <Legend wrapperStyle={{ color: '#9aa6ba' }} />
                <ReferenceLine y={0} stroke="#1f2735" />
                <Bar dataKey="predicted" name="Avg predicted" fill="#4c9aff" radius={[4, 4, 0, 0]} />
                <Bar dataKey="observed" name="Observed" fill="#38d996" radius={[4, 4, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        ) : (
          <EmptyState
            title="No evaluated predictions yet"
            description="Seed demo matches with final results to compute calibration bins."
          />
        )}
      </section>

      <section className="grid grid--2up" style={{ marginTop: 24 }}>
        <div className="card">
          <div className="card__header">
            <h2>By forecast minute</h2>
            <span className="card__subtitle">bucketed</span>
          </div>
          {report?.byMinuteBucket?.length ? (
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ color: '#6c7891', fontSize: 12, textAlign: 'left', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                  <th style={{ padding: '8px 10px' }}>Bucket</th>
                  <th>Brier</th>
                  <th>Log loss</th>
                  <th>n</th>
                </tr>
              </thead>
              <tbody>
                {report.byMinuteBucket.map((b) => (
                  <tr key={b.minuteBucket} style={{ borderTop: '1px solid #1f2735', fontFeatureSettings: '"tnum"' }}>
                    <td style={{ padding: '8px 10px' }}>{b.minuteBucket}</td>
                    <td>{b.brierScore.toFixed(3)}</td>
                    <td>{b.logLoss.toFixed(3)}</td>
                    <td>{b.sampleSize}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <EmptyState title="No bucketed evaluation yet" />
          )}
        </div>

        <div className="card">
          <div className="card__header">
            <h2>Benchmarks</h2>
            <span className="card__subtitle">
              {report ? `missing coverage: ${(report.missingBenchmarkCoverage * 100).toFixed(0)}%` : ''}
            </span>
          </div>
          {report?.benchmarks?.length ? (
            <table style={{ width: '100%', borderCollapse: 'collapse' }}>
              <thead>
                <tr style={{ color: '#6c7891', fontSize: 12, textAlign: 'left', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
                  <th style={{ padding: '8px 10px' }}>Provider</th>
                  <th>Horizon</th>
                  <th>Brier</th>
                  <th>Log loss</th>
                  <th>Acc</th>
                  <th>n</th>
                </tr>
              </thead>
              <tbody>
                {report.benchmarks.map((b) => (
                  <tr key={`${b.provider}-${b.horizon}`} style={{ borderTop: '1px solid #1f2735', fontFeatureSettings: '"tnum"' }}>
                    <td style={{ padding: '8px 10px' }}><strong>{b.provider}</strong></td>
                    <td>{b.horizon}</td>
                    <td>{b.brierScore.toFixed(3)}</td>
                    <td>{b.logLoss.toFixed(3)}</td>
                    <td>{pct(b.accuracy)}</td>
                    <td>{b.sampleSize}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <EmptyState title="No benchmark coverage yet" />
          )}
        </div>
      </section>
    </>
  );
}
