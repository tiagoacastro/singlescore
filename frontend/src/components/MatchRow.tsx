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

const KICKOFF_FORMAT = new Intl.DateTimeFormat(undefined, {
  weekday: 'short',
  day: 'numeric',
  month: 'short',
});

export function MatchRow({ match, showProbabilities = false }: MatchRowProps) {
  const home = match.homeTeam.shortName ?? match.homeTeam.name;
  const away = match.awayTeam.shortName ?? match.awayTeam.name;
  // Pre-match odds: later snapshots of a finished match converge on
  // the result (100% for the winner), which says nothing about the forecast.
  const prob = match.preMatchPrediction;

  return (
    <Link to={`/matches/${match.id}`} className="match-row" aria-label={`${home} vs ${away}`}>
      <div className="match-row__team">
        <span>{home}</span>
      </div>
      <div className="match-row__score">
        <div className="match-row__meta">
          {match.competition.name} · {KICKOFF_FORMAT.format(new Date(match.kickoffAt))}
        </div>
        <div className="score">
          {match.homeScore} <span style={{ opacity: 0.4 }}>·</span> {match.awayScore}
        </div>
        <div className="status" style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
          <MatchStatusChip status={match.status} />
          {/* Everything is historical now; only flag the exceptions. */}
          {match.provenance !== 'HISTORICAL' ? <ProvenanceChip provenance={match.provenance} /> : null}
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
