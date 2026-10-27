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
      <div className="scoreline">
        <div className="scoreline__team scoreline__team--home">{match.homeTeam.name}</div>
        <div className="scoreline__score">
          {match.homeScore} <span className="scoreline__sep">·</span> {match.awayScore}
        </div>
        <div className="scoreline__team">{match.awayTeam.name}</div>
      </div>
      {preMatch ? (
        <ProbTriplet pHome={preMatch.pHome} pDraw={preMatch.pDraw} pAway={preMatch.pAway} long />
      ) : (
        <EmptyState title="No pre-match prediction" />
      )}
    </div>
  );
}
