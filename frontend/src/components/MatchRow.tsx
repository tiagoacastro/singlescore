import { Link } from 'react-router-dom';
import type { Match } from '@/graphql/types';
import { MatchStatusChip, ProvenanceChip } from './Chip';

interface MatchRowProps {
  match: Match;
  showProbabilities?: boolean;
}

function pct(p: number): string {
  return `${Math.round(p * 100)}%`;
}

export function MatchRow({ match, showProbabilities = false }: MatchRowProps) {
  const home = match.homeTeam.shortName ?? match.homeTeam.name;
  const away = match.awayTeam.shortName ?? match.awayTeam.name;
  const prob = match.latestModelPrediction;

  return (
    <Link to={`/matches/${match.id}`} className="match-row" aria-label={`${home} vs ${away}`}>
      <div className="match-row__team">
        <span>{home}</span>
      </div>
      <div className="match-row__score">
        <div className="score">
          {match.homeScore} <span style={{ opacity: 0.4 }}>·</span> {match.awayScore}
        </div>
        <div className="status" style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          <MatchStatusChip status={match.status} minute={match.minute} />
          <ProvenanceChip provenance={match.provenance} />
        </div>
      </div>
      <div className="match-row__team match-row__team--away">
        <span>{away}</span>
      </div>
      {showProbabilities && prob ? (
        <div className="prob-triplet" style={{ gridColumn: '1 / -1' }}>
          <div className="prob prob--home">
            <span className="prob__label">Home</span>
            <span className="prob__value">{pct(prob.pHome)}</span>
          </div>
          <div className="prob prob--draw">
            <span className="prob__label">Draw</span>
            <span className="prob__value">{pct(prob.pDraw)}</span>
          </div>
          <div className="prob prob--away">
            <span className="prob__label">Away</span>
            <span className="prob__value">{pct(prob.pAway)}</span>
          </div>
        </div>
      ) : null}
    </Link>
  );
}
