# GraphQL overview

The GraphQL schema lives in `backend/internal/graph/schema.graphqls`. It is
served from `POST /graphql` by `handler.NewDefaultServer` (gqlgen) wrapped
as an `http.Handler` so no third-party router is needed.

## Core operations

- `health: String!` — simple liveness check.
- `dashboard: DashboardData!` — everything the overview dashboard needs in
  one round-trip.
- `competitions: [Competition!]!`
- `matches(status, limit, after): MatchConnection!` — cursor-based pagination.
- `match(id: ID!): Match` — includes events, model predictions and
  benchmark predictions.
- `modelEvaluation(modelName, modelVersion): EvaluationReport!` — Brier,
  log loss, accuracy, calibration bins and per-benchmark comparison.

## Pagination

Cursors are opaque base64-encoded `kickoff_at|id` tuples. The server
returns `endCursor` and `hasNextPage` in `PageInfo`. Clients should treat
cursors as opaque strings and never parse them.

## Error handling

Resolvers return typed Go errors. gqlgen converts them to GraphQL errors
with the field path. Unexpected errors are logged with a correlation id
and returned to the client with a generic "internal error" message.

## N+1 avoidance

Match-level nested fields (`events`, `modelPredictions`,
`benchmarkPredictions`) are hydrated with batched repository queries that
accept a list of match ids and return a per-match map, so a list of 20
matches hits SingleStore 1 time per field, not 20.
