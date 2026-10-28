import {
  type TooltipProps,
  Area,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import type { EvaluationReport } from '@/graphql/types';
import { colors } from '@/theme';

type Point = {
  minute: number;
  model: number;
  asItStands: number;
  band: [number, number]; // 95% interval for `model`
  n: number;
};

const minuteLabel = (m: number) => (m === 0 ? 'Pre-match' : m === 45 ? 'Half-time' : `${m}'`);

// 95% normal-approximation interval for a proportion, in percent.
function interval(p: number, n: number): [number, number] {
  const half = 1.96 * Math.sqrt((p * (1 - p)) / n);
  return [Math.max(0, (p - half) * 100), Math.min(100, (p + half) * 100)];
}

function PointTooltip({ active, payload }: TooltipProps<number, string>) {
  const p = active ? (payload?.[0]?.payload as Point | undefined) : undefined;
  if (!p) return null;
  return (
    <div className="chart-tooltip">
      <strong>{minuteLabel(p.minute)}</strong>
      <span>
        Model&apos;s pick right: {p.model.toFixed(1)}%{' '}
        <span className="muted">
          ({p.band[0].toFixed(0)}–{p.band[1].toFixed(0)}%)
        </span>
      </span>
      <span>Result as it stands held: {p.asItStands.toFixed(1)}%</span>
      <span className="muted">{p.n} matches</span>
    </div>
  );
}

// AccuracyByMinuteChart: at each point of the match, how often the
// model's most likely outcome turned out to be the final result, next to
// how often the score at that point already was.
export function AccuracyByMinuteChart({ rows }: { rows: EvaluationReport['byMinute'] }) {
  const data: Point[] = rows.map((r) => ({
    minute: r.minute,
    model: r.accuracy * 100,
    asItStands: r.asItStandsAccuracy * 100,
    band: interval(r.accuracy, r.sampleSize),
    n: r.sampleSize,
  }));
  return (
    <div className="chart-wrap">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={data} margin={{ top: 10, right: 24, bottom: 20, left: 0 }}>
          <CartesianGrid stroke={colors.border} strokeDasharray="3 3" />
          <XAxis
            dataKey="minute"
            type="number"
            domain={[0, 85]}
            ticks={[0, 15, 30, 45, 60, 75, 85]}
            tickFormatter={(v) => (v === 0 ? 'KO' : v === 45 ? 'HT' : `${v}'`)}
            stroke={colors.textMuted}
          />
          <YAxis domain={[0, 100]} ticks={[0, 25, 50, 75, 100]} tickFormatter={(v) => `${v}%`} stroke={colors.textMuted} />
          <Tooltip content={<PointTooltip />} cursor={{ stroke: colors.border }} />
          <Legend wrapperStyle={{ color: colors.textSecondary }} />
          <Area
            dataKey="band"
            name="95% range"
            stroke="none"
            fill={colors.home}
            fillOpacity={0.15}
            isAnimationActive={false}
            legendType="none"
          />
          <Line
            dataKey="model"
            name="Model's pick was right"
            stroke={colors.home}
            strokeWidth={2}
            dot={{ r: 3 }}
            isAnimationActive={false}
          />
          <Line
            dataKey="asItStands"
            name="Result as it stands held"
            stroke={colors.textSecondary}
            strokeDasharray="5 4"
            dot={false}
            isAnimationActive={false}
          />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  );
}
