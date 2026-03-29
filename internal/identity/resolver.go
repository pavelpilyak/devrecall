package identity

import (
	"github.com/pavelpiliak/devrecall/internal/storage"
)

// Resolver merges identities across different sources using email as the primary key.
type Resolver struct {
	db *storage.DB
}

// NewResolver creates an identity resolver backed by the given database.
func NewResolver(db *storage.DB) *Resolver {
	return &Resolver{db: db}
}
