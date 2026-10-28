import { useQuery } from '@apollo/client';
import {
  type TooltipProps,
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { MODEL_EVALUATION_QUERY } from '@/graphql/queries';
import type { EvaluationReport } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { formatPct } from '@/lib/format';
import { colors } from '@/theme';

// What guessing a third for each outcome would score.
const UNIFORM = { brier: 2 / 3, logLoss: Math.log(3), accuracy: 1 / 3 };

const PROVIDER_LABELS: Record<string, string> = {
  'football-data-couk-market-avg': "Bookmakers' average odds",
  'football-data-couk-bet365': 'Bet365 odds',
};

const MINUTE_LABELS: Record<string, string> = {
  'pre-match': 'Pre-match',
  '0-14': "0–14'",
  '15-29': "15–29'",
  '30-44': "30–44'",
  '45-59': "45–59'",
  '60-74': "60–74'",
  '75+': "75'+",
};

const num = (x: number | null | undefined) => (x == null ? '—' : x.toFixed(3));
const pct = (x: number | null | undefined) => (x == null ? '—' : formatPct(x, 1));
const dateLabel = (iso?: string | null) =>
  iso ? new Date(iso).toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) : '—';

type Benchmark = EvaluationReport['benchmarks'][number];

type CalibrationPoint = { predicted: number; observed: number; n: number; bin: string };

function CalibrationTooltip({ active, payload }: TooltipProps<number, string>) {
  const p = active ? (payload?.[0]?.payload as CalibrationPoint | undefined) : undefined;
  if (!p) return null;
  return (
    <div className="chart-tooltip">
      <strong>Forecasts of {p.bin}</strong>
      <span>
        avg {p.predicted}% → happened {p.observed}%
      </span>
      <span className="muted">{p.n} forecasts</span>
    </div>
  );
}

// The market benchmark to compare against: the pre-match odds source
// covering the most matches.
function marketBenchmark(report: EvaluationReport | undefined): Benchmark | null {
  const pre = (report?.benchmarks ?? []).filter((b) => b.horizon === 'PRE_MATCH');
  return pre.reduce<Benchmark | null>((best, b) => (!best || b.sampleSize > best.sampleSize ? b : best), null);
}

type CompareProps = {
  title: string;
  model?: number;
  market?: number | null;
  uniform: number;
  format: (x: number | null | undefined) => string;
  higherIsBetter?: boolean;
};

// Headline metric: the model's value, then the market and pure guessing
// on the same matches for scale.
function CompareCard({ title, model, market, uniform, format, higherIsBetter = false }: CompareProps) {
  const beatsMarket =
    model != null && market != null && (higherIsBetter ? model > market : model < market);
  return (
    <div className="card">
      <div className="card__header">
        <h3>{title}</h3>
        <span className="card__subtitle">{higherIsBetter ? 'higher is better' : 'lower is better'}</span>
      </div>
      <div className="metric">
        <span className="metric__value">{format(model)}</span>
        <span className="metric__meta">
          market{' '}
          <strong className={market == null ? undefined : beatsMarket ? 'text-ok' : 'text-bad'}>
            {format(market)}
          </strong>{' '}
          · guessing {format(uniform)}
        </span>
      </div>
    </div>
  );
}

export function EvaluationPage() {
  const { data, loading, error } = useQuery<{ modelEvaluation: EvaluationReport }>(
    MODEL_EVALUATION_QUERY,
    { errorPolicy: 'all' },
  );
  const report = data?.modelEvaluation;
  const summary = report?.summary ?? null;
  const market = marketBenchmark(report);
  const calibration: CalibrationPoint[] = (report?.calibration ?? [])
    .filter((b) => b.sampleSize > 0)
    .map((b) => ({
      predicted: Math.round(b.avgPredicted * 1000) / 10,
      observed: Math.round(b.observedFrequency * 1000) / 10,
      n: b.sampleSize,
      bin: `${Math.round(b.binLow * 100)}–${Math.round(b.binHigh * 100)}%`,
    }));

  return (
    <>
      <header className="page-header">
        <div>
          <h1>Model evaluation</h1>
          {summary ? (
            <p className="page-header__intro">
              {summary.modelName} {summary.modelVersion}, scored on its pre-match forecast for{' '}
              {summary.sampleSize} finished matches ({dateLabel(summary.dateRangeFrom)} –{' '}
              {dateLabel(summary.dateRangeTo)}), against the bookmakers&apos; pre-match odds for the
              same matches.
            </p>
          ) : null}
        </div>
      </header>

      {error ? <div className="error-banner">GraphQL error: {error.message}</div> : null}

      <section className="grid grid--3up" aria-label="Headline metrics">
        <CompareCard title="Brier score" model={summary?.brierScore} market={market?.brierScore}
          uniform={UNIFORM.brier} format={num} />
        <CompareCard title="Log loss" model={summary?.logLoss} market={market?.logLoss}
          uniform={UNIFORM.logLoss} format={num} />
        <CompareCard title="Picked the result" model={summary?.accuracy} market={market?.accuracy}
          uniform={UNIFORM.accuracy} format={pct} higherIsBetter />
      </section>

      <section className="card section-gap">
        <div className="card__header">
          <h2>Calibration</h2>
          <span className="card__subtitle">when the model says X%, how often it happens</span>
        </div>
        {loading && !report ? (
          <div className="skeleton skeleton--block" />
        ) : calibration.length ? (
          <>
            <div className="chart-wrap">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={calibration} margin={{ top: 10, right: 24, bottom: 20, left: 0 }}>
                  <CartesianGrid stroke={colors.border} strokeDasharray="3 3" />
                  <XAxis
                    dataKey="predicted"
                    type="number"
                    domain={[0, 100]}
                    ticks={[0, 20, 40, 60, 80, 100]}
                    tickFormatter={(v) => `${v}%`}
                    stroke={colors.textMuted}
                    label={{ value: 'Forecast probability', position: 'insideBottom', offset: -10, fill: colors.textMuted, fontSize: 12 }}
                  />
                  <YAxis
                    type="number"
                    domain={[0, 100]}
                    ticks={[0, 20, 40, 60, 80, 100]}
                    tickFormatter={(v) => `${v}%`}
                    stroke={colors.textMuted}
                  />
                  <ReferenceLine
                    segment={[{ x: 0, y: 0 }, { x: 100, y: 100 }]}
                    stroke={colors.textMuted}
                    strokeDasharray="4 4"
                    ifOverflow="extendDomain"
                  />
                  <Tooltip cursor={{ stroke: colors.border }} content={<CalibrationTooltip />} />
                  <Line
                    type="linear"
                    dataKey="observed"
                    name="Observed"
                    stroke={colors.home}
                    strokeWidth={2}
                    dot={{ r: 4 }}
                    isAnimationActive={false}
                  />
                </LineChart>
              </ResponsiveContainer>
            </div>
            <p className="card__note">
              Every forecast contributes its home, draw and away probabilities. On the dashed
              diagonal the model is perfectly calibrated; points above it mean those outcomes
              happened more often than forecast (the model was too cautious), below it less often.
            </p>
          </>
        ) : (
          <EmptyState
            title="No evaluated predictions yet"
            description="Calibration needs finished matches with a pre-match prediction."
          />
        )}
      </section>

      <section className="card section-gap">
        <div className="card__header">
          <h2>By league</h2>
          <span className="card__subtitle">pre-match, model vs market on the same matches</span>
        </div>
        {report?.byCompetition.length ? (
          <table className="data-table">
            <thead>
              <tr>
                <th>League</th>
                <th>Matches</th>
                <th>Model Brier</th>
                <th>Market Brier</th>
                <th>Model − market</th>
                <th>Model log loss</th>
                <th>Picked the result</th>
              </tr>
            </thead>
            <tbody>
              {report.byCompetition.map((c) => {
                const gap = c.marketBrierScore == null ? null : c.brierScore - c.marketBrierScore;
                return (
                  <tr key={c.competition.id}>
                    <td>
                      <strong>{c.competition.name}</strong>{' '}
                      {c.competition.country ? <span className="muted">{c.competition.country}</span> : null}
                    </td>
                    <td>{c.sampleSize}</td>
                    <td>{num(c.brierScore)}</td>
                    <td>{num(c.marketBrierScore)}</td>
                    <td className={gap == null ? undefined : gap <= 0 ? 'text-ok' : 'text-bad'}>
                      {gap == null ? '—' : `${gap > 0 ? '+' : ''}${gap.toFixed(3)}`}
                    </td>
                    <td>{num(c.logLoss)}</td>
                    <td>{pct(c.accuracy)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        ) : (
          <EmptyState title="No competitions evaluated yet" />
        )}
      </section>

      <section className="grid grid--2up grid--even section-gap">
        <div className="card">
          <div className="card__header">
            <h2>By match minute</h2>
            <span className="card__subtitle">in-play snapshots</span>
          </div>
          {report?.byMinuteBucket.length ? (
            <>
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Minute</th>
                    <th>Brier</th>
                    <th>Log loss</th>
                    <th>Forecasts</th>
                  </tr>
                </thead>
                <tbody>
                  {report.byMinuteBucket.map((b) => (
                    <tr key={b.minuteBucket}>
                      <td>{MINUTE_LABELS[b.minuteBucket] ?? b.minuteBucket}</td>
                      <td>{num(b.brierScore)}</td>
                      <td>{num(b.logLoss)}</td>
                      <td>{b.sampleSize}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="card__note">
                Every snapshot of every match: the 5-minute grid, half-time and each goal or red
                card. Scores fall as the match goes on because less is left to happen; full-time
                snapshots aren&apos;t scored (they already know the result).
              </p>
            </>
          ) : (
            <EmptyState title="No in-play snapshots yet" />
          )}
        </div>

        <div className="card">
          <div className="card__header">
            <h2>Benchmarks</h2>
            <span className="card__subtitle">
              {report ? `odds for ${formatPct(1 - report.missingBenchmarkCoverage)} of matches` : ''}
            </span>
          </div>
          {report?.benchmarks.length ? (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Source</th>
                  <th>Brier</th>
                  <th>Log loss</th>
                  <th>Picked</th>
                  <th>Matches</th>
                </tr>
              </thead>
              <tbody>
                {summary ? (
                  <tr>
                    <td>
                      <strong>This model</strong> <span className="muted">pre-match</span>
                    </td>
                    <td>{num(summary.brierScore)}</td>
                    <td>{num(summary.logLoss)}</td>
                    <td>{pct(summary.accuracy)}</td>
                    <td>{summary.sampleSize}</td>
                  </tr>
                ) : null}
                {report.benchmarks.map((b) => (
                  <tr key={`${b.provider}-${b.horizon}`}>
                    <td>
                      <strong>{PROVIDER_LABELS[b.provider] ?? b.provider}</strong>{' '}
                      <span className="muted">pre-match, de-vigged</span>
                    </td>
                    <td>{num(b.brierScore)}</td>
                    <td>{num(b.logLoss)}</td>
                    <td>{pct(b.accuracy)}</td>
                    <td>{b.sampleSize}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <EmptyState title="No benchmark forecasts captured" />
          )}
        </div>
      </section>
    </>
  );
}
