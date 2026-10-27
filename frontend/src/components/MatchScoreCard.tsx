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
  const latest = preds.length
    ? [...preds].sort((a, b) => (b.matchMinute ?? 0) - (a.matchMinute ?? 0))[0]
    : null;

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
