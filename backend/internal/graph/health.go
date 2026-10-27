package graph

import (
	"context"

	"github.com/tiagocastro/singlescore/backend/internal/db"
	"github.com/tiagocastro/singlescore/backend/internal/graph/model"
)

// buildHealth re-checks DB reachability on each call so the dashboard
// reflects the current state rather than the startup snapshot.
//
// Lives outside schema.resolvers.go because gqlgen's regenerator moves
// non-resolver helpers it doesn't recognise into a `/* dead code */`
// block on each run. Keeping it here keeps the resolver file
// regenerable without manual re-stitching.
func (r *queryResolver) buildHealth(ctx context.Context) *model.Health {
	reachable := db.Ping(ctx, r.deps.Pool) == nil
	status := "ok"
	if !reachable {
		status = "degraded"
	}
	return &model.Health{
		Status:            status,
		DatabaseReachable: reachable,
		Version:           r.deps.Version,
		StartedAt:         r.deps.StartedAt,
	}
}
