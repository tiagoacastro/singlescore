import { useMemo } from 'react';
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  CartesianGrid,
  ResponsiveContainer,
  Legend,
  ReferenceDot,
} from 'recharts';
import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';

// Statuses whose scoreline can still change — unused here but kept
// alongside the chart's live-related concerns for proximity. Note:
// polling cadence is owned by the page, not the chart.

type ChartPoint = {
  minute: number;
  forecastAt: string;
  home: number;
  draw: number;
  away: number;
};

type Marker = {
  key: string;
  minute: number;
  kind: 'GOAL' | 'RED_CARD';
  teamSide: 'HOME' | 'AWAY' | null;
  description: string;
};

type Props = {
  match: Pick<Match, 'modelPredictions' | 'events'>;
};

// ProbabilityTimeline renders the per-minute probability-evolution
// chart plus goal / red-card markers along the X-axis.
//
// Data is derived from the match's `modelPredictions` (collapsed to
// one point per unique minute — in-play ticks that landed on the same
// minute keep only the last write) and `events` (filtered to goals +
// red cards, clamped to the 0-90 chart domain).
export function ProbabilityTimeline({ match }: Props) {
  // chartData + markers are derived; useMemo stops Recharts from
  // re-mounting on every parent re-render (e.g. a 12 s poll tick).
  //
  // Filter: when multiple model versions coexist in the DB (we swap the
  // baseline from time to time), always render only the newest one so
  // the line doesn't flip between formulas minute-to-minute. "Newest"
  // = the (name, version) pair with the max forecast_at timestamp.
  const chartData: ChartPoint[] = useMemo(() => {
    const all = match.modelPredictions ?? [];
    if (all.length === 0) return [];
    // Pick the latest-active (name, version).
    const latest = all.reduce((best, p) =>
      !best || p.forecastAt > best.forecastAt ? p : best,
    );
    const activeName = latest.modelName;
    const activeVersion = latest.modelVersion;
    const byMinute = new Map<number, ChartPoint>();
    all
      .filter((p) => p.modelName === activeName && p.modelVersion === activeVersion)
      .forEach((p) => {
        const minute = p.matchMinute ?? 0;
        byMinute.set(minute, {
          minute,
          forecastAt: p.forecastAt,
          home: Math.round(p.pHome * 1000) / 10,
          draw: Math.round(p.pDraw * 1000) / 10,
          away: Math.round(p.pAway * 1000) / 10,
        });
      });
    return Array.from(byMinute.values()).sort((a, b) => a.minute - b.minute);
  }, [match.modelPredictions]);

  const markers: Marker[] = useMemo(
    () =>
      (match.events ?? [])
        .filter(
          (ev): ev is NonNullable<typeof ev> =>
            ev != null &&
            (ev.eventType === 'GOAL' || ev.eventType === 'RED_CARD') &&
            ev.minute != null,
        )
        .map((ev) => ({
          key: ev.id,
          // Clamp to the chart's 0-90 X-axis. Stoppage-time events
          // pin to 45 or 90 because that's where the probability
          // snapshot was taken too.
          minute: Math.min(90, Math.max(0, ev.minute ?? 0)),
          kind: ev.eventType as 'GOAL' | 'RED_CARD',
          teamSide: (ev.teamSide as 'HOME' | 'AWAY' | null) ?? null,
          description: ev.description ?? '',
        })),
    [match.events],
  );

  // Which model is actually driving the plot (surfaced in the header
  // so viewers can tell what formula they're looking at).
  const activeModel = useMemo(() => {
    const all = match.modelPredictions ?? [];
    if (all.length === 0) return null;
    const latest = all.reduce((best, p) =>
      !best || p.forecastAt > best.forecastAt ? p : best,
    );
    return { name: latest.modelName, version: latest.modelVersion };
  }, [match.modelPredictions]);

  if (chartData.length === 0) {
    return (
      <>
        <div className="card__header">
          <h2>Probability timeline</h2>
        </div>
        <EmptyState
          title="No prediction snapshots yet"
          description="Model snapshots are generated at kickoff, half-time and full-time, plus every live minute while the match is in play."
        />
      </>
    );
  }

  return (
    <>
    <div className="card__header">
      <h2>Probability timeline</h2>
      <span className="card__subtitle">
        {chartData.length} snapshots
        {activeModel ? ` · ${activeModel.name} ${activeModel.version}` : ''}
      </span>
    </div>
    <div className="chart-wrap">
      <ResponsiveContainer width="100%" height="100%">
        <LineChart data={chartData} margin={{ top: 10, right: 20, bottom: 20, left: 0 }}>
          <CartesianGrid stroke="#1f2735" strokeDasharray="3 3" />
          <XAxis
            dataKey="minute"
            type="number"
            domain={[0, 90]}
            ticks={[0, 15, 30, 45, 60, 75, 90]}
            stroke="#6c7891"
            tickFormatter={(v) => (v === 0 ? 'KO' : v === 90 ? 'FT' : `${v}'`)}
            label={{ value: 'Match minute', position: 'insideBottom', offset: -5, fill: '#6c7891', fontSize: 12 }}
          />
          <YAxis stroke="#6c7891" domain={[0, 100]} tickFormatter={(v) => `${v}%`} />
          <Tooltip
            contentStyle={{ background: '#141924', border: '1px solid #1f2735', borderRadius: 8 }}
            formatter={(v: number) => `${v.toFixed(1)}%`}
            labelFormatter={(l) => (l === 0 ? 'Pre-match' : l === 90 ? 'Full-time' : `Minute ${l}`)}
          />
          <Legend wrapperStyle={{ color: '#9aa6ba' }} />
          {/* Dots are enabled so the kickoff/HT/FT hard snapshots are
              visually obvious even when there are only 3 points. */}
          <Line type="monotone" dataKey="home" stroke="#38d996" dot={{ r: 3 }} activeDot={{ r: 5 }} name="Home" />
          <Line type="monotone" dataKey="draw" stroke="#9aa6ba" dot={{ r: 3 }} activeDot={{ r: 5 }} name="Draw" />
          <Line type="monotone" dataKey="away" stroke="#ff7fa3" dot={{ r: 3 }} activeDot={{ r: 5 }} name="Away" />
          {/* Goal + red-card markers along the X-axis. Only rendered
              when we actually have event data for this match
              (api-football provides this on recent matches only —
              free-tier 3-day sliding window). */}
          {markers.map((mk) => (
            <ReferenceDot
              key={mk.key}
              x={mk.minute}
              y={0}
              ifOverflow="extendDomain"
              isFront
              shape={(props) => (
                <EventMarkerShape
                  cx={(props as { cx?: number }).cx}
                  cy={(props as { cy?: number }).cy}
                  payload={mk}
                />
              )}
            />
          ))}
        </LineChart>
      </ResponsiveContainer>
    </div>
    </>
  );
}

// EventMarkerShape renders one event (goal or red card) as an inline
// SVG glyph above the X-axis, with the match minute written directly
// underneath. Rendered via Recharts' <ReferenceDot shape={...}/> so
// the (minute, 0) coordinate is translated into chart pixels for us.
//
// Props shape matches what Recharts injects: {cx, cy, payload: our
// extraProps}. We anchor to a pixel offset above the X-axis so
// markers aren't eaten by the probability lines or axis labels.
type MarkerShapeProps = {
  cx?: number;
  cy?: number;
  payload?: Marker;
};
function EventMarkerShape({ cx, cy, payload }: MarkerShapeProps) {
  if (cx == null || !payload) return null;
  const anchorY = (cy ?? 0) + 18;
  const isGoal = payload.kind === 'GOAL';
  const sideColor =
    payload.teamSide === 'HOME' ? '#38d996' : payload.teamSide === 'AWAY' ? '#ff7fa3' : '#9aa6ba';
  return (
    <g>
      <title>{`${payload.minute}' — ${payload.kind === 'GOAL' ? 'Goal' : 'Red card'}${
        payload.description ? ' — ' + payload.description : ''
      }`}</title>
      {isGoal ? (
        // Soccer-ball-ish glyph: a filled circle with a small inner
        // dot. Scales cleanly at any dpr.
        <>
          <circle cx={cx} cy={anchorY - 10} r={7} fill="#fff" stroke={sideColor} strokeWidth={2} />
          <circle cx={cx} cy={anchorY - 10} r={2.4} fill={sideColor} />
        </>
      ) : (
        // Red card: a solid rounded rectangle.
        <rect
          x={cx - 5}
          y={anchorY - 17}
          width={10}
          height={14}
          rx={1.5}
          fill="#ef4a56"
          stroke="#1a0a0c"
          strokeWidth={0.75}
        />
      )}
      <text
        x={cx}
        y={anchorY + 10}
        textAnchor="middle"
        fontSize={10}
        fontFamily="system-ui, sans-serif"
        fill="#9aa6ba"
        style={{ fontFeatureSettings: '"tnum"' }}
      >
        {payload.minute}&apos;
      </text>
    </g>
  );
}
