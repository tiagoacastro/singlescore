import { useMemo } from 'react';
import type { Match, MatchCommentary } from '@/graphql/types';
import { EVENT_KINDS, SHOT_KINDS } from '@/lib/events';
import { formatMinute } from '@/lib/format';
import { isGoal } from '@/lib/timeline';
import { colors } from '@/theme';

type Props = {
  match: Pick<Match, 'commentary' | 'homeTeam' | 'awayTeam'>;
};

// Minute window: regulation plus 10 minutes of stoppage covers nearly
// every match; later minutes are clamped to the right edge.
const X_MIN = 0;
const X_MAX = 100;
const WIDTH = 1130;
const HEIGHT = 170;
const PAD_X = 96;
const PAD_Y = 30;
const CENTER_Y = HEIGHT / 2;
const LANE_OFFSET = 24; // home dots above the baseline, away below
const MINUTE_TICKS = [0, 15, 30, 45, 60, 75, 90];

// X position: second-half stoppage extends past 90 (90'+5 -> 95), but
// first-half stoppage stays on the 45 line so it can't be drawn after
// half-time.
function effectiveMinute(c: MatchCommentary): number {
  const m = c.minute ?? 0;
  const firstHalf = c.period === 1 || (c.period == null && m <= 45);
  const x = firstHalf ? Math.min(m, 45) : m + (c.addedMinute ?? 0);
  return Math.min(X_MAX, Math.max(X_MIN, x));
}

const xScale = (minute: number) =>
  PAD_X + ((minute - X_MIN) / (X_MAX - X_MIN)) * (WIDTH - PAD_X * 2);

function laneY(side: string | null | undefined): number {
  return side === 'HOME' ? CENTER_Y - LANE_OFFSET : side === 'AWAY' ? CENTER_Y + LANE_OFFSET : CENTER_Y;
}

// OpportunityTimeline is a shot map along the match clock: every attempt
// and goal from the commentary, home above the baseline and away below,
// sized and coloured by outcome. Hidden when the match has no shot
// commentary.
export function OpportunityTimeline({ match }: Props) {
  const shots = useMemo(
    () => (match.commentary ?? []).filter((r) => SHOT_KINDS.has(r.kind)),
    [match.commentary],
  );
  if (shots.length === 0) return null;

  const count = (side: string) => shots.filter((s) => s.teamSide === side).length;
  const goals = shots.filter((s) => isGoal({ eventType: s.kind })).length;
  const short = (team: Props['match']['homeTeam']) =>
    team.shortName ?? team.name.slice(0, 3).toUpperCase();

  return (
    <section className="card">
      <div className="card__header">
        <h2>Opportunity map</h2>
        <span className="card__subtitle">
          {shots.length} shot{shots.length === 1 ? '' : 's'} · {goals} goal{goals === 1 ? '' : 's'}
        </span>
      </div>

      <div className="shot-map">
        <svg
          width="100%"
          height={HEIGHT}
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
          preserveAspectRatio="xMidYMid meet"
          role="img"
          aria-label={`Shots by minute: ${match.homeTeam.name} ${count('HOME')}, ${match.awayTeam.name} ${count('AWAY')}`}
        >
          <line x1={PAD_X} x2={WIDTH - PAD_X} y1={CENTER_Y} y2={CENTER_Y} stroke="#2a3245" />
          {MINUTE_TICKS.map((m) => (
            <g key={m}>
              <line
                x1={xScale(m)}
                x2={xScale(m)}
                y1={PAD_Y}
                y2={HEIGHT - PAD_Y}
                stroke="#1e2434"
                strokeDasharray={m === 0 || m === 90 ? undefined : '2 3'}
              />
              <text x={xScale(m)} y={HEIGHT - 10} fill={colors.textMuted} fontSize="10" textAnchor="middle">
                {m}&apos;
              </text>
            </g>
          ))}
          {/* Half-time divider */}
          <line
            x1={xScale(45)}
            x2={xScale(45)}
            y1={PAD_Y}
            y2={HEIGHT - PAD_Y}
            stroke="#3a4660"
            strokeDasharray="4 4"
          />
          <text x={PAD_X - 8} y={laneY('HOME') - 4} fill={colors.textSecondary} fontSize="10" textAnchor="end">
            {short(match.homeTeam)}
          </text>
          <text x={PAD_X - 8} y={laneY('AWAY') + 12} fill={colors.textSecondary} fontSize="10" textAnchor="end">
            {short(match.awayTeam)}
          </text>
          {shots.map((s) => {
            const spec = EVENT_KINDS[s.kind];
            const sided = s.teamSide === 'HOME' || s.teamSide === 'AWAY';
            return (
              <circle
                key={s.sequence}
                cx={xScale(effectiveMinute(s))}
                cy={laneY(s.teamSide)}
                r={spec.shotRadius}
                fill={spec.color}
                opacity={sided ? 0.85 : 0.6}
              >
                <title>{`${formatMinute(s.minute, s.addedMinute)} · ${spec.label}\n${s.text}`}</title>
              </circle>
            );
          })}
        </svg>
      </div>

      <div className="legend">
        {[...SHOT_KINDS]
          .filter((k) => shots.some((s) => s.kind === k))
          .map((k) => {
            const spec = EVENT_KINDS[k];
            const d = (spec.shotRadius ?? 4) * 2;
            return (
              <span key={k} className="legend__item">
                <span className="legend__dot" style={{ width: d, height: d, background: spec.color }} />
                {spec.label}
              </span>
            );
          })}
      </div>
    </section>
  );
}
