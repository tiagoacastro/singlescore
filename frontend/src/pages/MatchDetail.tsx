import { useEffect } from 'react';
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

// Statuses whose scoreline can still change upstream and are therefore
// worth live-polling. Everything else (final, pre-kickoff, postponed,
// cancelled) is static and must not consume API quota via per-match
// refreshes.
const LIVE_STATUSES: ReadonlySet<string> = new Set(['LIVE', 'HALFTIME']);

// Poll cadence for live matches. Paired with the backend's per-match
// RefreshWindow (10 s) so N concurrent viewers share one upstream call.
const LIVE_POLL_INTERVAL_MS = 12000;

export function MatchDetailPage() {
  const { id } = useParams<{ id: string }>();
  // One-shot fetch; polling starts only when the loaded match is
  // in-play, and stops as soon as it isn't (or on unmount).
  const { data, loading, error, startPolling, stopPolling } = useQuery<{
    match: Match | null;
  }>(MATCH_DETAIL_QUERY, {
    variables: { id },
    errorPolicy: 'all',
  });

  const status = data?.match?.status;
  useEffect(() => {
    if (status && LIVE_STATUSES.has(status)) {
      // Backend's match(id) resolver transparently re-fetches upstream
      // (deduped per match by RefreshWindow) so the scoreline updates
      // in near-real-time while the user watches a live match.
      startPolling(LIVE_POLL_INTERVAL_MS);
      return () => stopPolling();
    }
    // Non-live status: cancel any prior poll from this component
    // (e.g. match transitioned LIVE → FINISHED while open).
    stopPolling();
    return undefined;
  }, [status, startPolling, stopPolling]);

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
          <MatchStatusChip status={m.status} minute={m.minute} />
          <ProvenanceChip provenance={m.provenance} />
        </div>
      </header>

      <section className="grid grid--2up">
        <MatchScoreCard match={m} />
        <MatchEventList match={m} />
      </section>

      <section className="card" style={{ marginTop: 24 }}>
        <ProbabilityTimeline match={m} />
      </section>

      <OpportunityTimeline match={m} />

      <MatchStatsPanel match={m} />

      <MatchCommentaryPanel match={m} />

      <BenchmarkTable match={m} />
    </>
  );
}
