import type { Match, MatchEvent, ModelPrediction } from '@/graphql/types';

// Predictions of the newest model (name + version by latest forecast).
// Mixing versions would make the line jump between formulas.
export function activeModelPredictions(preds: readonly ModelPrediction[]): ModelPrediction[] {
  let latest: ModelPrediction | null = null;
  for (const p of preds) {
    if (!latest || Date.parse(p.forecastAt) > Date.parse(latest.forecastAt)) latest = p;
  }
  if (!latest) return [];
  const { modelName, modelVersion } = latest;
  return preds.filter((p) => p.modelName === modelName && p.modelVersion === modelVersion);
}

export type TimelineEvent = Pick<MatchEvent, 'id' | 'eventType' | 'description'> & {
  teamSide: 'HOME' | 'AWAY' | 'NEUTRAL' | null;
  minute: number | null;
  addedMinute: number | null;
  // Derived from a scoreline step between two snapshots rather than a
  // per-event feed, so only the side is certain.
  approximate: boolean;
};

const GOAL_TYPES: ReadonlySet<string> = new Set(['GOAL', 'OWN_GOAL']);

export function isGoal(e: Pick<MatchEvent, 'eventType'>): boolean {
  return GOAL_TYPES.has(e.eventType);
}

// On-pitch order: minute, then stoppage time (45'+3 before 46').
export function compareMatchTime(
  a: { minute?: number | null; addedMinute?: number | null },
  b: { minute?: number | null; addedMinute?: number | null },
): number {
  return (a.minute ?? 0) - (b.minute ?? 0) || (a.addedMinute ?? 0) - (b.addedMinute ?? 0);
}

type TimelineInput = Pick<
  Match,
  'events' | 'modelPredictions' | 'homeScore' | 'awayScore' | 'homeTeam' | 'awayTeam'
>;

// The match's event rows, in on-pitch order. Real per-event data is used
// as-is when its goals add up to the final score (own goals are already
// credited to the benefiting side). Otherwise the goals are derived from
// scoreline steps in the model snapshots (same rule as the ingest), while
// the real cards, subs and VAR rows are kept.
export function buildTimelineEvents(match: TimelineInput): TimelineEvent[] {
  const real: TimelineEvent[] = (match.events ?? []).map((e) => ({
    id: e.id,
    eventType: e.eventType,
    teamSide: e.teamSide ?? null,
    description: e.description,
    minute: e.minute ?? null,
    addedMinute: e.addedMinute ?? null,
    approximate: false,
  }));
  const goals = real.filter(isGoal);
  const consistent =
    goals.filter((g) => g.teamSide === 'HOME').length === match.homeScore &&
    goals.filter((g) => g.teamSide === 'AWAY').length === match.awayScore;
  const rows = consistent ? real : [...real.filter((e) => !isGoal(e)), ...derivedGoals(match)];
  return rows.sort(compareMatchTime);
}

function derivedGoals(match: TimelineInput): TimelineEvent[] {
  const snapshots = activeModelPredictions(match.modelPredictions ?? []).sort(
    (a, b) => (a.matchMinute ?? 0) - (b.matchMinute ?? 0),
  );
  const out: TimelineEvent[] = [];
  let prevHome = 0;
  let prevAway = 0;
  for (const p of snapshots) {
    const minute = p.matchMinute ?? 0;
    const steps: ['HOME' | 'AWAY', number, string][] = [
      ['HOME', p.homeScoreAtForecast - prevHome, match.homeTeam.name],
      ['AWAY', p.awayScoreAtForecast - prevAway, match.awayTeam.name],
    ];
    for (const [side, n, team] of steps) {
      for (let g = 0; g < n; g++) {
        out.push({
          id: `derived-${side}-${minute}-${g}`,
          eventType: 'GOAL',
          teamSide: side,
          description: `${team} — exact minute unknown`,
          minute,
          addedMinute: null,
          approximate: true,
        });
      }
    }
    prevHome = p.homeScoreAtForecast;
    prevAway = p.awayScoreAtForecast;
  }
  return out;
}
