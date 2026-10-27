# Architecture

## System overview

```
┌──────────────────┐    GraphQL/HTTP      ┌──────────────────┐
│  React (frontend)│ ───────────────────▶│  Go backend      │
│  Apollo Client   │                      │  gqlgen + net/   │
│  React Router    │ ◀───────────────────│  http            │
└──────────────────┘   JSON responses     └────────┬─────────┘
                                                   │ MySQL wire (TLS)
                                                   ▼
                                          ┌──────────────────┐
                                          │  SingleStoreDB   │
                                          │  v10.8 cluster   │
                                          └──────────────────┘
```

Ingestion runs inside the backend process (goroutine, configurable
schedule). Each run writes to `data_ingestion_runs` for observability.

## Backend layers

- `cmd/server/main.go` — composition root. Loads config, builds the DB pool,
  wires GraphQL resolvers, starts `http.Server` with graceful shutdown.
- `internal/config` — environment loading with safe defaults.
- `internal/db` — SingleStore connection pool, health probe, transaction
  helpers. All queries take `context.Context` with deadlines.
- `internal/graph` — gqlgen schema, generated code, hand-written resolvers.
- `internal/repository` — table-focused data access (one file per table).
- `internal/service` — business services (match querying, dashboard,
  evaluation reports) that compose repositories.
- `internal/prediction` — the `Predictor` interface plus the baseline
  Elo-based implementation. Easy to swap for a trained model later.
- `internal/evaluation` — multiclass Brier score, log loss, accuracy and
  calibration bins with unit tests. Does not perform I/O.
- `internal/ingestion` — `Provider` interface and adapters (demo provider
  for now; football-data.org adapter is planned).

The backend uses only `net/http`; gqlgen's `handler.Server` is an
`http.Handler`, which satisfies this constraint.

## Data model

Core entities (see `migrations/0001_init.sql`):

- `competitions` and `teams` are **reference tables**: small, read-mostly,
  and replicated to every leaf. This lets us use rich unique constraints.
- `matches`, `match_events`, `model_predictions`, `benchmark_predictions`,
  `prediction_evaluations` and `data_ingestion_runs` are **rowstore**
  tables sharded by `id` (large per-match data colocates well in a
  single-node starter cluster; we'll revisit shard keys for multi-leaf
  scale-out).
- Timestamps are `DATETIME(6)` in UTC.
- Probabilities are validated in Go (sum ≈ 1.0 within tolerance, each in
  `[0, 1]`). SingleStore accepts the writes; the application is the source
  of truth for the invariant.
- Actual outcomes live on `matches.final_result` and must only be filled
  from authoritative sources *after* the match ends, so they can never
  leak into forecast inputs.

## GraphQL conventions

- Schema-first via gqlgen.
- Cursor-based pagination (`MatchConnection`, opaque base64 cursors).
- Enums map 1:1 with SingleStore ENUM columns.
- Resolver layer stays thin; business logic lives in `internal/service`.

## Prediction + evaluation flow

```
seed/ingest ──▶ matches, match_events
                        │
                        ▼
            prediction.Predictor
                        │
                        ▼
            model_predictions (snapshots)
                        │                 ┌── benchmark_predictions
                        ▼                 ▼
   evaluation.Compute ◀──────── matches.final_result
                        │
                        ▼
          prediction_evaluations (per-forecast metrics)
                        │
                        ▼
         service/evaluation → GraphQL report
```

Evaluations are only produced for matches with `status = 'FINISHED'` and a
non-null `final_result`. Benchmarks are compared on the eligible overlap of
matches; the GraphQL report always exposes `sampleSize` and
`missingBenchmarkCoverage` so under-coverage is never hidden.
