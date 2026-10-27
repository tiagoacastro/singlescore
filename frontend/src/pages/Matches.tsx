import { NetworkStatus, useQuery } from '@apollo/client';
import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { COMPETITIONS_QUERY, MATCH_LIST_QUERY } from '@/graphql/queries';
import type { Competition, Match } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { MatchRow } from '@/components/MatchRow';
import { useDebouncedValue } from '@/lib/useDebouncedValue';

interface MatchesData {
  matches: {
    totalCount: number;
    pageInfo: { endCursor?: string | null; hasNextPage: boolean };
    edges: { cursor: string; node: Match }[];
  };
}

const PAGE_SIZE = 20;

// Filters live in the URL (?competition=&team=) so they survive opening a
// match and coming back.
export function MatchesPage() {
  const [params, setParams] = useSearchParams();
  const competitionId = params.get('competition') || null;
  const [teamInput, setTeamInput] = useState(params.get('team') ?? '');
  const team = useDebouncedValue(teamInput.trim(), 300);

  const setParam = (key: string, value: string | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value) next.set(key, value);
        else next.delete(key);
        return next;
      },
      { replace: true },
    );

  useEffect(() => {
    if (team !== (params.get('team') ?? '')) setParam('team', team || null);
    // Only the debounced input writes the URL.
  }, [team]);

  const { data: compData } = useQuery<{ competitions: Competition[] }>(COMPETITIONS_QUERY);
  const { data, loading, error, fetchMore, networkStatus } = useQuery<MatchesData>(
    MATCH_LIST_QUERY,
    {
      variables: { competitionId, team: team || null, first: PAGE_SIZE },
      errorPolicy: 'all',
      notifyOnNetworkStatusChange: true,
    },
  );
  const matches = data?.matches;
  const loadingMore = networkStatus === NetworkStatus.fetchMore;
  const filtered = Boolean(competitionId || team);

  return (
    <>
      <header className="page-header">
        <h1>Matches</h1>
      </header>

      <div className="filter-bar" role="search">
        <label className="field">
          <span className="field__label">Competition</span>
          <select
            className="field__control"
            value={competitionId ?? ''}
            onChange={(e) => setParam('competition', e.target.value || null)}
          >
            <option value="">All competitions</option>
            {compData?.competitions.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
                {c.country ? ` (${c.country})` : ''}
              </option>
            ))}
          </select>
        </label>
        <label className="field field--grow">
          <span className="field__label">Team</span>
          <input
            className="field__control"
            type="search"
            placeholder="Search by team name"
            value={teamInput}
            onChange={(e) => setTeamInput(e.target.value)}
          />
        </label>
        {filtered ? (
          <button
            type="button"
            className="chip filter-bar__clear"
            onClick={() => {
              setTeamInput('');
              setParams(new URLSearchParams(), { replace: true });
            }}
          >
            Clear filters
          </button>
        ) : null}
      </div>

      {error ? <div className="error-banner">GraphQL error: {error.message}</div> : null}

      <section className="card">
        <div className="card__header">
          <h2>Match list</h2>
          <span className="card__subtitle">
            {matches ? `${matches.edges.length} of ${matches.totalCount}` : '…'}
          </span>
        </div>
        {loading && !matches ? (
          <>
            <div className="skeleton skeleton--row" />
            <div className="skeleton skeleton--row" />
            <div className="skeleton skeleton--row" />
          </>
        ) : matches?.edges.length ? (
          <>
            <div className="stack stack--tight">
              {matches.edges.map((e) => (
                <MatchRow key={e.node.id} match={e.node} showProbabilities />
              ))}
            </div>
            {matches.pageInfo.hasNextPage ? (
              <button
                type="button"
                className="chip load-more"
                disabled={loadingMore}
                onClick={() =>
                  fetchMore({ variables: { after: matches.pageInfo.endCursor } })
                }
              >
                {loadingMore ? 'Loading…' : `Load more (${matches.totalCount - matches.edges.length} left)`}
              </button>
            ) : null}
          </>
        ) : (
          <EmptyState
            title="No matches found"
            description={filtered ? 'Try another competition or team name.' : undefined}
          />
        )}
      </section>
    </>
  );
}
