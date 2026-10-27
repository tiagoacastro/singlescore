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
    errorPolicy: 'all',
  });

  if (loading && !data) {
    return (
      <section className="card">
        <div className="skeleton skeleton--row" />
        <div className="skeleton skeleton--block" style={{ marginTop: 16 }} />
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
          <div style={{ fontSize: 12, color: '#6c7891', textTransform: 'uppercase', letterSpacing: '0.08em' }}>
            {m.competition.name}
            {m.competition.country ? ` · ${m.competition.country}` : ''}
          </div>
          <h1>
            {m.homeTeam.name} <span style={{ color: '#6c7891' }}>vs</span> {m.awayTeam.name}
          </h1>
        </div>
        <div className="page-header__meta">
          <MatchStatusChip status={m.status} />
          <ProvenanceChip provenance={m.provenance} />
        </div>
      </header>

      <div className="match-layout">
        <div>
          <MatchScoreCard match={m} />

          <section className="card" style={{ marginTop: 24 }}>
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
