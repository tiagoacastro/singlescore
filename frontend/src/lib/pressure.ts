import type { MatchCommentary } from '@/graphql/types';

// Pressure weight of one commentary line. Mirrors action_weight in
// scripts/ingest_season.py, which feeds the same index into the model.
const WEIGHTS: Record<string, number> = {
  GOAL: 1.0,
  ATTEMPT_SAVED: 0.6,
  ATTEMPT_MISSED: 0.35,
  ATTEMPT_BLOCKED: 0.3,
  PENALTY_MISSED: 0.8,
  PENALTY_SAVED: 0.8,
  CORNER: 0.35,
  OFFSIDE: 0.1,
};

export function actionWeight(kind: string, text: string): number {
  if (kind === 'FREE_KICK') return text.includes('attacking half') ? 0.2 : 0;
  let w = WEIGHTS[kind] ?? 0;
  if (kind === 'GOAL' || kind.startsWith('ATTEMPT')) {
    const t = text.toLowerCase();
    if (t.includes('very close range') || t.includes('six yard box')) w *= 1.5;
    else if (t.includes('outside the box') || t.includes('long range')) w *= 0.6;
  }
  return w;
}

export type Action = { x: number; side: 'HOME' | 'AWAY'; weight: number };

// Attacking actions placed on a 0-100 axis: first-half stoppage stays on
// 45, second-half stoppage runs past 90.
export function pressureActions(rows: readonly MatchCommentary[]): Action[] {
  const out: Action[] = [];
  for (const r of rows) {
    if (r.minute == null || (r.teamSide !== 'HOME' && r.teamSide !== 'AWAY')) continue;
    const weight = actionWeight(r.kind, r.text);
    if (!weight) continue;
    const firstHalf = r.period === 1 || (r.period == null && r.minute <= 45);
    const x = firstHalf ? Math.min(r.minute, 45) : Math.min(r.minute + (r.addedMinute ?? 0), 100);
    out.push({ x, side: r.teamSide, weight });
  }
  return out;
}
