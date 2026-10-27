import { useQuery } from '@apollo/client';
import { useParams } from 'react-router-dom';
import { MATCH_DETAIL_QUERY } from '@/graphql/queries';
import type { Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { MatchStatusChip, ProvenanceChip } from '@/components/Chip';
import { MatchScoreCard } from '@/components/MatchScoreCard';
import { MatchEventList } from '@/components/MatchEventList';
import { MatchStatsPanel } from '@/components/MatchStatsPanel';
import { MatchCommentaryPanel } from '@/components/MatchCommentaryPanel';
import { OpportunityTimeline } from '@/components/OpportunityTimeline';
import { ProbabilityTimeline } from '@/components/ProbabilityTimeline';
import { BenchmarkTable } from '@/components/BenchmarkTable';

export function MatchDetailPage() {
  const { id } = useParams<{ id: string }>();
  const { data, loading, error } = useQuery<{
    match: Match | null;
  }>(MATCH_DETAIL_QUERY, {
    variables: { id },
    skip: !id,
    errorPolicy: 'all',
  });

  if (loading && !data) {
    return (
      <section className="card">
        <div className="skeleton skeleton--row" />
        <div className="skeleton skeleton--block section-gap" />
      </section>
    );
  }
  if (error && !data?.match) {
    return <div className="error-banner">Error loading match: {error.message}</div>;
  }
  const m = data?.match;
  if (!m) {
    return <EmptyState title="Match not found" />;
  }

  return (
    <>
      <header className="page-header">
        <div>
          <div className="page-header__eyebrow">
            {m.competition.name}
            {m.competition.country ? ` · ${m.competition.country}` : ''}
          </div>
          <h1>
            {m.homeTeam.name} <span className="muted">vs</span> {m.awayTeam.name}
          </h1>
        </div>
        <div className="page-header__meta">
          <MatchStatusChip status={m.status} />
          <ProvenanceChip provenance={m.provenance} />
        </div>
      </header>

      {/* Keyed by match so per-panel state (e.g. the commentary filter)
          resets when navigating to another match. */}
      <div className="match-layout" key={m.id}>
        <div className="stack">
          <MatchScoreCard match={m} />

          <section className="card">
            <ProbabilityTimeline match={m} />
          </section>

          <OpportunityTimeline match={m} />

          <MatchStatsPanel match={m} />

          <MatchCommentaryPanel match={m} />

          <BenchmarkTable match={m} />
        </div>

        <MatchEventList match={m} />
      </div>
    </>
  );
}
