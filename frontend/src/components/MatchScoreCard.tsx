import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';

type Props = {
  match: Pick<Match, 'homeTeam' | 'awayTeam' | 'homeScore' | 'awayScore' | 'kickoffAt' | 'preMatchPrediction'>;
};

function pct(p: number) {
  return `${Math.round(p * 100)}%`;
}

// MatchScoreCard shows the final scoreline next to the model's pre-match
// odds. Later snapshots of a finished match converge on the result
// (100% for the winner), so they say nothing about the forecast.
export function MatchScoreCard({ match }: Props) {
  const preMatch = match.preMatchPrediction;

  return (
    <div className="card">
      <div className="card__header">
        <h2>Score</h2>
        <span className="card__subtitle">
          kickoff {new Date(match.kickoffAt).toLocaleString()} · pre-match odds
        </span>
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
      {preMatch ? (
        <div className="prob-triplet">
          <div className="prob prob--home">
            <span className="prob__label">Home win</span>
            <span className="prob__value">{pct(preMatch.pHome)}</span>
          </div>
          <div className="prob prob--draw">
            <span className="prob__label">Draw</span>
            <span className="prob__value">{pct(preMatch.pDraw)}</span>
          </div>
          <div className="prob prob--away">
            <span className="prob__label">Away win</span>
            <span className="prob__value">{pct(preMatch.pAway)}</span>
          </div>
        </div>
      ) : (
        <EmptyState title="No pre-match prediction" />
      )}
    </div>
  );
}
