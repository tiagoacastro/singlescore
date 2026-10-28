import { useMemo } from 'react';
import { Bar, BarChart, Cell, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import type { Match } from '@/graphql/types';
import { pressureActions, type Action } from '@/lib/pressure';
import { colors } from '@/theme';

type Props = {
  match: Pick<Match, 'commentary' | 'homeTeam' | 'awayTeam'>;
};

const DECAY_MINUTES = 4; // how quickly a spell of pressure fades
const BLOCKS: [number, number, string][] = [
  [0, 15, "0–15'"], [15, 30, "15–30'"], [30, 45.5, "30–45'"],
  [45.5, 60, "45–60'"], [60, 75, "60–75'"], [75, 101, "75–90'+"],
];

function momentumSeries(actions: Action[]) {
  const last = Math.max(90, ...actions.map((a) => Math.ceil(a.x)));
  const series = [];
  for (let t = 1; t <= last; t++) {
    let value = 0;
    for (const a of actions) {
      if (a.x <= t) value += (a.side === 'HOME' ? 1 : -1) * a.weight * Math.exp(-(t - a.x) / DECAY_MINUTES);
    }
    series.push({ minute: t, value: Math.round(value * 100) / 100 });
  }
  return series;
}

// MomentumChart shows who was on top: shots (weighted by how close they
// were), corners, attacking free kicks and offsides, decayed over a few
// minutes. Bars above the line are home pressure, below are away. The
// model tilts its in-play odds by the same index. Hidden when the match
// has no commentary.
export function MomentumChart({ match }: Props) {
  const actions = useMemo(() => pressureActions(match.commentary ?? []), [match.commentary]);
  const series = useMemo(() => momentumSeries(actions), [actions]);
  if (actions.length === 0) return null;

  const blocks = BLOCKS.map(([from, to, label]) => {
    const inBlock = actions.filter((a) => a.x >= from && a.x < to);
    const home = inBlock.filter((a) => a.side === 'HOME').reduce((s, a) => s + a.weight, 0);
    const away = inBlock.filter((a) => a.side === 'AWAY').reduce((s, a) => s + a.weight, 0);
    const share = home + away > 0 ? home / (home + away) : 0.5;
    return { label, share };
  });
  const max = Math.max(1, ...series.map((p) => Math.abs(p.value)));

  return (
    <section className="card">
      <div className="card__header">
        <h2>Who&apos;s on top</h2>
        <span className="card__subtitle">momentum from shots, corners, attacking free kicks</span>
      </div>
      <div className="momentum">
        <ResponsiveContainer width="100%" height={150}>
          <BarChart data={series} margin={{ top: 6, right: 20, bottom: 0, left: 0 }} barCategoryGap={1}>
            <XAxis
              dataKey="minute"
              type="number"
              domain={[0, 'dataMax']}
              ticks={[0, 15, 30, 45, 60, 75, 90]}
              tickFormatter={(v) => `${v}'`}
              stroke={colors.textMuted}
              fontSize={11}
            />
            <YAxis hide domain={[-max, max]} />
            <ReferenceLine y={0} stroke={colors.border} />
            <ReferenceLine x={45.5} stroke="#3a4660" strokeDasharray="4 4" />
            <Tooltip
              cursor={{ fill: 'rgba(255,255,255,0.04)' }}
              contentStyle={{ background: colors.bgRaised, border: `1px solid ${colors.border}`, borderRadius: 8 }}
              labelFormatter={(l) => `Minute ${l}`}
              formatter={(v: number) => [
                v >= 0 ? match.homeTeam.name : match.awayTeam.name,
                Math.abs(v) < 0.05 ? 'Even' : 'On top',
              ]}
            />
            <Bar dataKey="value" isAnimationActive={false}>
              {series.map((p) => (
                <Cell key={p.minute} fill={p.value >= 0 ? colors.home : colors.away} />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>
      <div className="momentum-blocks">
        {blocks.map((b) => {
          const side = b.share > 0.55 ? 'home' : b.share < 0.45 ? 'away' : null;
          const team = side === 'home' ? match.homeTeam.name : side === 'away' ? match.awayTeam.name : 'Even';
          const pct = Math.round((side === 'away' ? 1 - b.share : b.share) * 100);
          return (
            <div key={b.label} className={`momentum-blocks__item${side ? ` momentum-blocks__item--${side}` : ''}`}>
              <span className="momentum-blocks__label">{b.label}</span>
              <span className="momentum-blocks__team">{team}</span>
              {side ? <span className="momentum-blocks__share">{pct}%</span> : null}
            </div>
          );
        })}
      </div>
    </section>
  );
}
