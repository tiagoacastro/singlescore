import { useQuery } from '@apollo/client';
import { useState } from 'react';
import { MATCH_LIST_QUERY } from '@/graphql/queries';
import type { Match, MatchStatus } from '@/graphql/types';
import { EmptyState } from '@/components/EmptyState';
import { MatchRow } from '@/components/MatchRow';

interface MatchesData {
  matches: {
    totalCount: number;
    pageInfo: { endCursor?: string | null; hasNextPage: boolean };
    edges: { cursor: string; node: Match }[];
  };
}

const STATUS_FILTERS: { label: string; value: MatchStatus | null }[] = [
  { label: 'All', value: null },
  { label: 'Scheduled', value: 'SCHEDULED' },
  { label: 'Finished', value: 'FINISHED' },
];

export function MatchesPage() {
  const [status, setStatus] = useState<MatchStatus | null>(null);
  const { data, loading, error, fetchMore } = useQuery<MatchesData>(MATCH_LIST_QUERY, {
    variables: { status, first: 20 },
    errorPolicy: 'all',
  });

  return (
    <>
      <header className="page-header">
        <h1>Matches</h1>
        <div className="page-header__meta">
          {STATUS_FILTERS.map((f) => (
            <button
              key={f.label}
              className={`chip${status === f.value ? ' chip--demo' : ''}`}
              onClick={() => setStatus(f.value)}
            >
              {f.label}
            </button>
          ))}
        </div>
      </header>

      {error ? (
        <div className="error-banner">GraphQL error: {error.message}</div>
      ) : null}

      <section className="card">
        <div className="card__header">
          <h2>Match list</h2>
          <span className="card__subtitle">
            {data?.matches.totalCount ?? 0} total
          </span>
        </div>
        {loading && !data ? (
          <>
            <div className="skeleton skeleton--row" />
            <div className="skeleton skeleton--row" />
            <div className="skeleton skeleton--row" />
          </>
        ) : data?.matches.edges.length ? (
          <>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {data.matches.edges.map((e) => (
                <MatchRow key={e.node.id} match={e.node} showProbabilities />
              ))}
            </div>
            {data.matches.pageInfo.hasNextPage ? (
              <button
                className="chip"
                style={{ alignSelf: 'center', marginTop: 12 }}
                onClick={() =>
                  fetchMore({
                    variables: { after: data.matches.pageInfo.endCursor },
                  })
                }
              >
                Load more
              </button>
            ) : null}
          </>
        ) : (
          <EmptyState
            title="No matches match this filter"
            description="Try switching the filter above."
          />
        )}
      </section>
    </>
  );
}
