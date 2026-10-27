import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';

type Props = {
  match: Pick<Match, 'homeTeam' | 'awayTeam' | 'homeScore' | 'awayScore' | 'kickoffAt' | 'modelPredictions'>;
};

function pct(p: number) {
  return `${Math.round(p * 100)}%`;
}

// MatchScoreCard shows the current scoreline and the "latest" model
// probabilities. "Latest" = the snapshot at the highest minute present
// in modelPredictions, which the user intuitively expects to see at
// the top of a live match (last in-play tick) or a finished match
// (full-time snapshot).
export function MatchScoreCard({ match }: Props) {
  const preds = match.modelPredictions ?? [];
  // When multiple model versions coexist, pin to the newest one before
  // picking the highest-minute row — otherwise the triplet can
  // disagree with the chart (which also filters to the latest model).
  const latest = (() => {
    if (preds.length === 0) return null;
    const newest = preds.reduce((best, p) =>
      !best || p.forecastAt > best.forecastAt ? p : best,
    );
    const active = preds.filter(
      (p) => p.modelName === newest.modelName && p.modelVersion === newest.modelVersion,
    );
    return active.sort((a, b) => (b.matchMinute ?? 0) - (a.matchMinute ?? 0))[0];
  })();

  return (
    <div className="card">
      <div className="card__header">
        <h2>Score</h2>
        <span className="card__subtitle">kickoff {new Date(match.kickoffAt).toLocaleString()}</span>
      </div>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: '1fr auto 1fr',
          alignItems: 'center',
          gap: 24,
        }}
      >
        <div style={{ textAlign: 'right', fontSize: 18 }}>{match.homeTeam.name}</div>
        <div style={{ fontSize: 44, fontWeight: 700, fontFeatureSettings: '"tnum"' }}>
          {match.homeScore} <span style={{ opacity: 0.4 }}>·</span> {match.awayScore}
        </div>
        <div style={{ fontSize: 18 }}>{match.awayTeam.name}</div>
      </div>
      {latest ? (
        <div className="prob-triplet">
          <div className="prob prob--home">
            <span className="prob__label">Home win</span>
            <span className="prob__value">{pct(latest.pHome)}</span>
          </div>
          <div className="prob prob--draw">
            <span className="prob__label">Draw</span>
            <span className="prob__value">{pct(latest.pDraw)}</span>
          </div>
          <div className="prob prob--away">
            <span className="prob__label">Away win</span>
            <span className="prob__value">{pct(latest.pAway)}</span>
          </div>
        </div>
      ) : (
        <EmptyState title="No model prediction yet" />
      )}
    </div>
  );
}
