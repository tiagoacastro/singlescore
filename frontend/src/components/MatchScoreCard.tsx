import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { ProbTriplet } from '@/components/ProbTriplet';

type Props = {
  match: Pick<Match, 'homeTeam' | 'awayTeam' | 'homeScore' | 'awayScore' | 'kickoffAt' | 'preMatchPrediction'>;
};

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
        <ProbTriplet pHome={preMatch.pHome} pDraw={preMatch.pDraw} pAway={preMatch.pAway} long />
      ) : (
        <EmptyState title="No pre-match prediction" />
      )}
    </div>
  );
}
