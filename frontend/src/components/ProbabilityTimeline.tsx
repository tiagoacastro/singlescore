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
  // Scoreline at this snapshot — used by the goal-marker derivation
  // to detect transitions when per-event data isn't available.
  homeScore: number;
  awayScore: number;
};

type Marker = {
  key: string;
  minute: number;
  kind: 'GOAL' | 'RED_CARD';
  teamSide: 'HOME' | 'AWAY' | null;
  description: string;
  // When true, the marker's minute is approximate (derived from a
  // scoreline transition between two grid snapshots rather than from
  // a per-event source). The tooltip surfaces this so the viewer
  // isn't misled.
  approximate?: boolean;
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
          homeScore: p.homeScoreAtForecast ?? 0,
          awayScore: p.awayScoreAtForecast ?? 0,
        });
      });
    return Array.from(byMinute.values()).sort((a, b) => a.minute - b.minute);
  }, [match.modelPredictions]);

  // Markers for the chart X-axis. Priority:
  //   1. Per-event data from api-football (precise minute + player +
  //      description). Available for matches inside the free-tier
  //      3-day window.
  //   2. Fallback: derive goal markers from scoreline transitions
  //      between consecutive chart snapshots. Minute is approximate
  //      (the step appears at the next grid point after the real
  //      goal) but the fact and side are correct. Honest-labelled
  //      via the "approximate" flag so the tooltip can note it.
  const markers: Marker[] = useMemo(() => {
    const apiEvents = (match.events ?? []).filter(
      (ev): ev is NonNullable<typeof ev> =>
        ev != null &&
        (ev.eventType === 'GOAL' || ev.eventType === 'OWN_GOAL' || ev.eventType === 'RED_CARD') &&
        ev.minute != null,
    );
    const hasApiGoals = apiEvents.some((e) => e.eventType !== 'RED_CARD');
    if (hasApiGoals) {
      return apiEvents.map((ev) => ({
        key: ev.id,
        // Clamp to the chart's 0-90 X-axis. Stoppage-time events pin
        // to 45 or 90 because that's where the probability snapshot
        // was taken too.
        minute: Math.min(90, Math.max(0, ev.minute ?? 0)),
        kind: ev.eventType === 'RED_CARD' ? 'RED_CARD' : 'GOAL',
        teamSide: (ev.teamSide as 'HOME' | 'AWAY' | null) ?? null,
        description: ev.description ?? '',
        approximate: false,
      }));
    }
    // Fallback: walk the sorted chart snapshots, emit a GOAL marker
    // for every net home / away score increment between consecutive
    // points. Works whenever modelPredictions carry homeScoreAtForecast
    // / awayScoreAtForecast — which they do for every FINISHED match
    // reconstructed from HT/FT scoreline (always present on
    // football-data.org's free tier).
    const derived: Marker[] = [];
    let prevH = 0;
    let prevA = 0;
    chartData.forEach((pt, i) => {
      if (i === 0) {
        prevH = pt.homeScore;
        prevA = pt.awayScore;
        return;
      }
      const dH = pt.homeScore - prevH;
      const dA = pt.awayScore - prevA;
      for (let g = 0; g < dH; g++) {
        derived.push({
          key: `derived-H-${pt.minute}-${g}`,
          minute: pt.minute,
          kind: 'GOAL',
          teamSide: 'HOME',
          description: 'exact minute unknown (no per-event data)',
          approximate: true,
        });
      }
      for (let g = 0; g < dA; g++) {
        derived.push({
          key: `derived-A-${pt.minute}-${g}`,
          minute: pt.minute,
          kind: 'GOAL',
          teamSide: 'AWAY',
          description: 'exact minute unknown (no per-event data)',
          approximate: true,
        });
      }
      prevH = pt.homeScore;
      prevA = pt.awayScore;
    });
    return derived;
  }, [match.events, chartData]);

  // When several markers land on the same minute (e.g. two goals in
  // the HT→FT delta of a match with no per-event data), spread them
  // out horizontally via an SVG-pixel offset so they don't overlap.
  // The underlying X-axis coordinate stays identical so the tooltip
  // and minute label are still correct.
  const markersWithOffset = useMemo(() => {
    const byMinute = new Map<number, Marker[]>();
    markers.forEach((m) => {
      const bucket = byMinute.get(m.minute) ?? [];
      bucket.push(m);
      byMinute.set(m.minute, bucket);
    });
    const out: (Marker & { pxOffset: number })[] = [];
    const spacingPx = 18;
    byMinute.forEach((bucket) => {
      const n = bucket.length;
      bucket.forEach((m, i) => {
        const centred = i - (n - 1) / 2;
        out.push({ ...m, pxOffset: centred * spacingPx });
      });
    });
    return out;
  }, [markers]);

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
          {markersWithOffset.map((mk) => (
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
  const approx = payload.approximate === true;
  const strokeDash = approx ? '2 2' : undefined;
  const minuteText = approx ? `~${payload.minute}'` : `${payload.minute}'`;
  const tooltipMinute = approx ? `~${payload.minute}'` : `${payload.minute}'`;
  return (
    <g>
      <title>{`${tooltipMinute} — ${payload.kind === 'GOAL' ? 'Goal' : 'Red card'}${
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
        {minuteText}
      </text>
    </g>
  );
}
