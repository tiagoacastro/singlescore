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
import { formatMinute } from '@/lib/format';
import { activeModelPredictions, buildTimelineEvents, isGoal } from '@/lib/timeline';

type ChartPoint = {
  minute: number;
  home: number;
  draw: number;
  away: number;
};

type Marker = {
  key: string;
  minute: number; // x position, clamped to the 0-90 axis
  label: string; // minute as shown, e.g. 45'+2
  kind: 'GOAL' | 'RED_CARD';
  teamSide: 'HOME' | 'AWAY' | 'NEUTRAL' | null;
  description: string;
  // Derived from a scoreline step rather than a per-event feed; drawn
  // dashed with a "~" minute.
  approximate: boolean;
};

type Props = {
  match: Pick<
    Match,
    'modelPredictions' | 'events' | 'homeScore' | 'awayScore' | 'homeTeam' | 'awayTeam'
  >;
};

// ProbabilityTimeline charts the newest model's home / draw / away
// probabilities by match minute, with goal and red-card markers along
// the X-axis.
export function ProbabilityTimeline({ match }: Props) {
  // Memoised so Recharts doesn't re-mount on unrelated re-renders.
  const { chartData, activeModel } = useMemo(() => {
    const active = activeModelPredictions(match.modelPredictions ?? []);
    // One point per minute; a later write at the same minute wins.
    const byMinute = new Map<number, ChartPoint>();
    for (const p of active) {
      const minute = p.matchMinute ?? 0;
      byMinute.set(minute, {
        minute,
        home: Math.round(p.pHome * 1000) / 10,
        draw: Math.round(p.pDraw * 1000) / 10,
        away: Math.round(p.pAway * 1000) / 10,
      });
    }
    return {
      chartData: [...byMinute.values()].sort((a, b) => a.minute - b.minute),
      activeModel: active[0] ? `${active[0].modelName} ${active[0].modelVersion}` : null,
    };
  }, [match.modelPredictions]);

  // Goal / red-card markers. Markers sharing a minute are spread out by
  // a pixel offset so they don't overlap (the x coordinate is unchanged).
  const markers = useMemo(() => {
    const out: Marker[] = buildTimelineEvents(match)
      .filter((e) => (isGoal(e) || e.eventType === 'RED_CARD') && e.minute != null)
      .map((e) => ({
        key: e.id,
        minute: Math.min(90, Math.max(0, e.minute ?? 0)),
        label: formatMinute(e.minute, e.addedMinute, e.approximate),
        kind: e.eventType === 'RED_CARD' ? 'RED_CARD' : 'GOAL',
        teamSide: e.teamSide,
        description: e.description ?? '',
        approximate: e.approximate,
      }));
    const byMinute = new Map<number, Marker[]>();
    for (const m of out) byMinute.set(m.minute, [...(byMinute.get(m.minute) ?? []), m]);
    const spacingPx = 18;
    return [...byMinute.values()].flatMap((bucket) =>
      bucket.map((m, i) => ({ ...m, pxOffset: (i - (bucket.length - 1) / 2) * spacingPx })),
    );
  }, [match]);

  if (chartData.length === 0) {
    return (
      <>
        <div className="card__header">
          <h2>Probability timeline</h2>
        </div>
        <EmptyState
          title="No prediction snapshots yet"
          description="Model snapshots are generated at kickoff, half-time and full-time, plus each goal or red card."
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
        {activeModel ? ` · ${activeModel}` : ''}
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
  payload?: Marker & { pxOffset?: number };
};
function EventMarkerShape({ cx, cy, payload }: MarkerShapeProps) {
  if (cx == null || !payload) return null;
  // Apply horizontal pixel offset so stacked markers at the same
  // minute don't visually overlap.
  const x = cx + (payload.pxOffset ?? 0);
  const anchorY = (cy ?? 0) + 18;
  const isGoal = payload.kind === 'GOAL';
  const sideColor =
    payload.teamSide === 'HOME' ? '#38d996' : payload.teamSide === 'AWAY' ? '#ff7fa3' : '#9aa6ba';
  // Approximate markers (derived from scoreline, no per-event source)
  // render with a dashed stroke and a tilde-prefixed minute label so
  // the viewer can distinguish them from precise ones.
  const approx = payload.approximate;
  const strokeDash = approx ? '2 2' : undefined;
  return (
    <g>
      <title>{`${payload.label} — ${payload.kind === 'GOAL' ? 'Goal' : 'Red card'}${
        payload.teamSide ? ` (${payload.teamSide})` : ''
      }${payload.description ? ' — ' + payload.description : ''}`}</title>
      {isGoal ? (
        <>
          <circle cx={x} cy={anchorY - 10} r={7} fill="#fff" stroke={sideColor} strokeWidth={2} strokeDasharray={strokeDash} />
          <circle cx={x} cy={anchorY - 10} r={2.4} fill={sideColor} />
        </>
      ) : (
        <rect
          x={x - 5}
          y={anchorY - 17}
          width={10}
          height={14}
          rx={1.5}
          fill="#ef4a56"
          stroke="#1a0a0c"
          strokeWidth={0.75}
          strokeDasharray={strokeDash}
        />
      )}
      <text
        x={x}
        y={anchorY + 10}
        textAnchor="middle"
        fontSize={10}
        fontFamily="system-ui, sans-serif"
        fill={approx ? '#6c7891' : '#9aa6ba'}
        fontStyle={approx ? 'italic' : 'normal'}
        style={{ fontFeatureSettings: '"tnum"' }}
      >
        {payload.label}
      </text>
    </g>
  );
}
