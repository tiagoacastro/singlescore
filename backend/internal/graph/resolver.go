// Package graph wires the gqlgen GraphQL schema to backend services.
//
// Resolvers live in generated-sibling files (*.resolvers.go). This file
// defines the Resolver root and the Deps it needs to serve queries.
package graph

import (
	"database/sql"
	"log/slog"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/service"
)

// Deps holds the collaborators the Resolver needs.
type Deps struct {
	Pool        *sql.DB
	Logger      *slog.Logger
	Service     *service.Service
	DemoMode    bool
	Version     string
	StartedAt   time.Time
	DBReachable bool
}

// Resolver is the gqlgen root resolver.
type Resolver struct {
	deps Deps
}

// NewResolver returns a Resolver with the supplied dependencies.
func NewResolver(d Deps) *Resolver {
	return &Resolver{deps: d}
}
