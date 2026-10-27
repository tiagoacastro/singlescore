# GraphQL overview

The GraphQL schema lives in `backend/internal/graph/schema.graphqls`. It is
served from `/graphql` (GET and POST only; the API is read-only) by a gqlgen
`handler.Server`, which is an `http.Handler`, so no third-party router is
needed.

## Core operations

- `health: Health!` — database reachability, version and start time.
- `dashboard: Dashboard!` — everything the overview dashboard needs in
  one round-trip.
- `competitions: [Competition!]!`
- `matches(status, competitionId, team, first, after): MatchConnection!` —
  newest first, filtered by competition and/or team name (case-insensitive
  substring), cursor-based pagination.
- `match(id: ID!): Match` — includes events, commentary, stats, model and
  benchmark predictions, and `preMatchPrediction`.
- `modelEvaluation(modelName, modelVersion): EvaluationReport!` — Brier,
  log loss, accuracy, calibration bins and per-benchmark comparison.

## Pagination

Cursors are opaque base64-encoded `kickoff_at|id` tuples. The server
returns `endCursor` and `hasNextPage` in `PageInfo`. Clients should treat
cursors as opaque strings and never parse them.

## Error handling

Resolver errors are logged server-side with the field path and returned
to the client as a generic "internal error" (so SQL details never leak).
Query validation errors are returned as-is.

## N+1 avoidance

List views (`matches`, `dashboard`) batch their lookups: teams,
competitions and `preMatchPrediction` are fetched once per page for all
match ids, not once per match. The heavier child collections (`events`,
`commentary`, `stats`, `modelPredictions`, `benchmarkPredictions`) are
only filled by `match(id)`; in list results they come back empty.
