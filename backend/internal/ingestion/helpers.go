package ingestion

import (
	"crypto/sha1"

	"github.com/google/uuid"
)

// deterministicID derives a stable UUIDv5-flavoured id from a (prefix,
// key) pair. Used so repeated syncs upsert the same row rather than
// inserting duplicates (because the pair always hashes to the same
// bytes). Prefix is a tiny domain tag ("match", "team", "prediction",
// …) that namespaces the hash so a team id and a match id with the
// same external string never collide.
func deterministicID(prefix, key string) string {
	h := sha1.Sum([]byte(prefix + "|" + key))
	id, err := uuid.FromBytes(h[:16])
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// strPtr returns a pointer to s.
func strPtr(s string) *string { return &s }

// strOrNil returns a pointer to s, or nil if s is empty. Used when
// writing nullable VARCHAR columns.
func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// strOrEmpty is the inverse of strOrNil: dereferences a nullable
// string, falling back to "" on nil.
func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// minuteOrZero dereferences a nullable match-minute, falling back to
// 0 (pre-match) on nil.
func minuteOrZero(m *int) int {
	if m == nil {
		return 0
	}
	return *m
}

// valueOrDefault returns v when non-zero, otherwise def. Used to
// substitute a sensible fallback for a team's Elo rating when the
// first-seen team row carries the zero value.
func valueOrDefault(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}
