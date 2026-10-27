import { useMemo } from 'react';
import type { Match, MatchCommentary } from '@/graphql/types';

type Props = {
  match: Pick<Match, 'commentary' | 'homeTeam' | 'awayTeam'>;
};

// Visual spec for each attempt kind. The y-axis is binary (home above
// the baseline, away below) and the circle colour / size encodes
// shot quality: GOAL biggest + green, saved mid + blue, blocked
// smaller + grey, missed small + muted. This gives a Flashscore-like
// shot map on the horizontal time axis.
const KIND_SPEC: Record<string, { label: string; color: string; radius: number }> = {
  GOAL: { label: 'Goal', color: '#38d996', radius: 7 },
  OWN_GOAL: { label: 'Own goal', color: '#ff7fa3', radius: 7 },
  PENALTY_MISSED: { label: 'Pen. missed', color: '#ef4a56', radius: 6 },
  PENALTY_SAVED: { label: 'Pen. saved', color: '#f2c94c', radius: 6 },
  ATTEMPT_SAVED: { label: 'Shot (saved)', color: '#7fb5ff', radius: 4.5 },
  ATTEMPT_BLOCKED: { label: 'Shot (blocked)', color: '#9aa6ba', radius: 4 },
  ATTEMPT_MISSED: { label: 'Shot (off target)', color: '#6c7891', radius: 3.5 },
};

const ATTEMPT_KINDS = new Set(Object.keys(KIND_SPEC));

// Minute window: regulation + 10 min of stoppage covers nearly every
// match. Anything past that is clamped so a late 90'+12 ends up at the
// right edge rather than falling off the chart.
const X_MIN = 0;
const X_MAX = 100;
const WIDTH = 1130;
const HEIGHT = 170;
const PAD_X = 96;
const PAD_Y = 30;

// X position: second-half stoppage extends past 90 (90'+5 -> 95), but
// first-half stoppage stays on the 45 line so it can't be drawn after
// half-time.
function effectiveMinute(c: MatchCommentary): number {
  const m = c.minute ?? 0;
  const firstHalf = c.period === 1 || (c.period == null && m <= 45);
  const x = firstHalf ? Math.min(m, 45) : m + (c.addedMinute ?? 0);
  return Math.min(X_MAX, Math.max(X_MIN, x));
}

// OpportunityTimeline renders a dot plot of every scoring attempt
// (saved / blocked / missed) + every actual goal plotted along the
// match minute axis. Home attempts are plotted above the baseline,
// away attempts below. Hidden when the match has no shot commentary
// rows (e.g. matches we haven't backfilled yet).
export function OpportunityTimeline({ match }: Props) {
  const rows = match.commentary ?? [];

  const attempts = useMemo(
    () =>
      rows
        .filter((r) => ATTEMPT_KINDS.has(r.kind))
        .map((r) => ({
          ...r,
          minuteEff: effectiveMinute(r),
        })),
    [rows],
  );

  if (attempts.length === 0) return null;

  const homeShots = attempts.filter((a) => a.teamSide === 'HOME');
  const awayShots = attempts.filter((a) => a.teamSide === 'AWAY');
  const neutralShots = attempts.filter(
    (a) => a.teamSide !== 'HOME' && a.teamSide !== 'AWAY',
  );
  const isGoal = (kind: string) => kind === 'GOAL' || kind === 'OWN_GOAL';
  const homeGoals = homeShots.filter((a) => isGoal(a.kind));
  const awayGoals = awayShots.filter((a) => isGoal(a.kind));

  const xScale = (minute: number) =>
    PAD_X + ((minute - X_MIN) / (X_MAX - X_MIN)) * (WIDTH - PAD_X * 2);
  const centerY = HEIGHT / 2;
  // How far above/below the centre the attempt dots live. Slight
  // jitter would help overlapping shots at the same minute but we
  // keep it simple for the MVP and let the opacity do the stacking
  // cue.
  const laneHeight = 48;

  // Minute gridlines every 15 (0, 15, 30, 45, 60, 75, 90).
  const minuteTicks = [0, 15, 30, 45, 60, 75, 90];

  return (
    <section className="card" style={{ marginTop: 24 }}>
      <div className="card__header">
        <h2>Opportunity map</h2>
        <span className="card__subtitle">
          {attempts.length} shot{attempts.length === 1 ? '' : 's'} ·{' '}
          {homeGoals.length + awayGoals.length} goal
          {homeGoals.length + awayGoals.length === 1 ? '' : 's'}
        </span>
      </div>

      <div style={{ overflowX: 'auto' }}>
        <svg
          width="100%"
          height={HEIGHT}
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
          preserveAspectRatio="xMidYMid meet"
          style={{ display: 'block', minWidth: 640 }}
          role="img"
          aria-label={`Shots by minute: ${match.homeTeam.name} ${homeShots.length}, ${match.awayTeam.name} ${awayShots.length}`}
        >
          {/* Baseline */}
          <line
            x1={PAD_X}
            x2={WIDTH - PAD_X}
            y1={centerY}
            y2={centerY}
            stroke="#2a3245"
            strokeWidth={1}
          />

          {/* Minute gridlines + labels */}
          {minuteTicks.map((m) => {
            const x = xScale(m);
            return (
              <g key={m}>
                <line
                  x1={x}
                  x2={x}
                  y1={PAD_Y}
                  y2={HEIGHT - PAD_Y}
                  stroke="#1e2434"
                  strokeWidth={1}
                  strokeDasharray={m === 0 || m === 90 ? undefined : '2 3'}
                />
                <text
                  x={x}
                  y={HEIGHT - 10}
                  fill="#6c7891"
                  fontSize="10"
                  textAnchor="middle"
                  style={{ fontFeatureSettings: '"tnum"' }}
                >
                  {m}
                  {"'"}
                </text>
              </g>
            );
          })}

          {/* Half-time divider */}
          <line
            x1={xScale(45)}
            x2={xScale(45)}
            y1={PAD_Y}
            y2={HEIGHT - PAD_Y}
            stroke="#3a4660"
            strokeWidth={1}
            strokeDasharray="4 4"
          />

          {/* Side labels */}
          <text
            x={PAD_X - 8}
            y={centerY - laneHeight / 2 - 4}
            fill="#9aa6ba"
            fontSize="10"
            textAnchor="end"
          >
            {match.homeTeam.shortName ?? match.homeTeam.name.slice(0, 3).toUpperCase()}
          </text>
          <text
            x={PAD_X - 8}
            y={centerY + laneHeight / 2 + 12}
            fill="#9aa6ba"
            fontSize="10"
            textAnchor="end"
          >
            {match.awayTeam.shortName ?? match.awayTeam.name.slice(0, 3).toUpperCase()}
          </text>

          {/* Attempt dots */}
          {homeShots.map((a, i) => {
            const spec = KIND_SPEC[a.kind]!;
            return (
              <circle
                key={`h-${i}-${a.sequence}`}
                cx={xScale(a.minuteEff)}
                cy={centerY - laneHeight / 2}
                r={spec.radius}
                fill={spec.color}
                opacity={0.85}
              >
                <title>
                  {`${minuteDisplay(a)} · ${spec.label}\n${a.text}`}
                </title>
              </circle>
            );
          })}
          {awayShots.map((a, i) => {
            const spec = KIND_SPEC[a.kind]!;
            return (
              <circle
                key={`a-${i}-${a.sequence}`}
                cx={xScale(a.minuteEff)}
                cy={centerY + laneHeight / 2}
                r={spec.radius}
                fill={spec.color}
                opacity={0.85}
              >
                <title>
                  {`${minuteDisplay(a)} · ${spec.label}\n${a.text}`}
                </title>
              </circle>
            );
          })}
          {neutralShots.map((a, i) => {
            const spec = KIND_SPEC[a.kind]!;
            return (
              <circle
                key={`n-${i}-${a.sequence}`}
                cx={xScale(a.minuteEff)}
                cy={centerY}
                r={spec.radius}
                fill={spec.color}
                opacity={0.6}
              >
                <title>
                  {`${minuteDisplay(a)} · ${spec.label}\n${a.text}`}
                </title>
              </circle>
            );
          })}
        </svg>
      </div>

      {/* Legend */}
      <div
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: 14,
          marginTop: 10,
          fontSize: 11,
          color: '#9aa6ba',
        }}
      >
        {Object.entries(KIND_SPEC)
          .filter(([k]) => attempts.some((a) => a.kind === k))
          .map(([k, spec]) => (
            <span key={k} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              <span
                style={{
                  width: spec.radius * 2,
                  height: spec.radius * 2,
                  borderRadius: '50%',
                  background: spec.color,
                  display: 'inline-block',
                }}
              />
              {spec.label}
            </span>
          ))}
      </div>
    </section>
  );
}

function minuteDisplay(c: MatchCommentary): string {
  const m = c.minute ?? 0;
  const a = c.addedMinute ?? 0;
  return a > 0 ? `${m}'+${a}` : `${m}'`;
}
