// Apollo Client wiring. The GraphQL endpoint can be overridden at build
// time via FRONTEND_GRAPHQL_ENDPOINT; defaults to the webpack dev-server
// proxy at /graphql.

import { ApolloClient, HttpLink, InMemoryCache, from } from '@apollo/client';
import { onError } from '@apollo/client/link/error';

type MatchConnectionRef = { edges: unknown[] } & Record<string, unknown>;

declare const process: { env: Record<string, string | undefined> };

const endpoint =
  (process.env.GRAPHQL_ENDPOINT as string | undefined) ?? '/graphql';

const httpLink = new HttpLink({
  uri: endpoint,
  credentials: 'omit',
});

const errorLink = onError(({ graphQLErrors, networkError, operation }) => {
  if (graphQLErrors) {
    for (const err of graphQLErrors) {
      // eslint-disable-next-line no-console
      console.error(
        `[GraphQL error] op=${operation.operationName ?? 'anonymous'} path=${err.path?.join('.')} message=${err.message}`,
      );
    }
  }
  if (networkError) {
    // eslint-disable-next-line no-console
    console.error(
      `[Network error] op=${operation.operationName ?? 'anonymous'}`,
      networkError,
    );
  }
});

export const apolloClient = new ApolloClient({
  link: from([errorLink, httpLink]),
  // Rely on Apollo's default `id`-based normalisation. If a selection
  // doesn't include `id`, Apollo embeds the object rather than erroring —
  // which is the right behaviour for lightweight fields like
  // `Team { shortName name }` used purely for display.
  cache: new InMemoryCache({
    typePolicies: {
      Query: {
        fields: {
          // One cached list per filter combination; `fetchMore` pages
          // (same filters, an `after` cursor) are appended to it.
          matches: {
            keyArgs: ['status', 'competitionId', 'team'],
            merge(existing: MatchConnectionRef | undefined, incoming: MatchConnectionRef, { args }) {
              if (!existing || !args?.after) return incoming;
              return { ...incoming, edges: [...existing.edges, ...incoming.edges] };
            },
          },
        },
      },
    },
  }),
  defaultOptions: {
    // Data only changes when the hourly notebooks run, so cached
    // results are reused while navigating.
    watchQuery: { fetchPolicy: 'cache-first', errorPolicy: 'all' },
    query: { fetchPolicy: 'network-only', errorPolicy: 'all' },
  },
});
