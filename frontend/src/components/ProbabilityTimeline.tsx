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
import { EventMarker, type Marker } from '@/components/EventMarker';
import { formatMinute } from '@/lib/format';
import { activeModelPredictions, buildTimelineEvents, isGoal } from '@/lib/timeline';
import { colors } from '@/theme';

type ChartPoint = {
  minute: number;
  home: number;
  draw: number;
  away: number;
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
          <CartesianGrid stroke={colors.border} strokeDasharray="3 3" />
          <XAxis
            dataKey="minute"
            type="number"
            domain={[0, 90]}
            ticks={[0, 15, 30, 45, 60, 75, 90]}
            stroke={colors.textMuted}
            tickFormatter={(v) => (v === 0 ? 'KO' : v === 90 ? 'FT' : `${v}'`)}
            label={{ value: 'Match minute', position: 'insideBottom', offset: -5, fill: colors.textMuted, fontSize: 12 }}
          />
          <YAxis stroke={colors.textMuted} domain={[0, 100]} tickFormatter={(v) => `${v}%`} />
          <Tooltip
            contentStyle={{ background: colors.bgRaised, border: `1px solid ${colors.border}`, borderRadius: 8 }}
            formatter={(v: number) => `${v.toFixed(1)}%`}
            labelFormatter={(l) => (l === 0 ? 'Pre-match' : l === 90 ? 'Full-time' : `Minute ${l}`)}
          />
          <Legend wrapperStyle={{ color: colors.textSecondary }} />
          {/* Dots are enabled so the kickoff/HT/FT hard snapshots are
              visually obvious even when there are only 3 points. */}
          <Line type="monotone" dataKey="home" stroke={colors.home} dot={{ r: 3 }} activeDot={{ r: 5 }} name="Home" />
          <Line type="monotone" dataKey="draw" stroke={colors.draw} dot={{ r: 3 }} activeDot={{ r: 5 }} name="Draw" />
          <Line type="monotone" dataKey="away" stroke={colors.away} dot={{ r: 3 }} activeDot={{ r: 5 }} name="Away" />
          {markers.map((mk) => (
            <ReferenceDot
              key={mk.key}
              x={mk.minute}
              y={0}
              ifOverflow="extendDomain"
              isFront
              shape={(props) => (
                <EventMarker
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
