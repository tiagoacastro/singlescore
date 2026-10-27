package repository

import "strings"

// inClause returns the "?,?,…" placeholder list for ids and the ids as
// query args, for `WHERE col IN (…)` lookups.
func inClause(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}
