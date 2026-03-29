package identity

import (
	"fmt"

	"github.com/pavelpiliak/devrecall/internal/storage"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

// Resolver merges identities across different sources using email as the primary key.
type Resolver struct {
	db *storage.DB
}

// NewResolver creates an identity resolver backed by the given database.
func NewResolver(db *storage.DB) *Resolver {
	return &Resolver{db: db}
}

// SetupSelf creates or updates the "self" identity using the given name and
// git author emails. Each email gets an identity_link with source="git".
// The first email is used as the primary identity email.
func (r *Resolver) SetupSelf(name string, emails []string) (*models.Identity, error) {
	if len(emails) == 0 {
		return nil, fmt.Errorf("at least one email is required")
	}

	// Create identity with the first email.
	primaryEmail := emails[0]
	id, err := r.db.InsertIdentity(name, primaryEmail, true)
	if err != nil {
		return nil, fmt.Errorf("create self identity: %w", err)
	}

	// Link all emails as git identity links.
	for _, email := range emails {
		if err := r.db.InsertIdentityLink(id, "git", email); err != nil {
			return nil, fmt.Errorf("link email %s: %w", email, err)
		}
	}

	return &models.Identity{
		ID:     id,
		Name:   name,
		Email:  primaryEmail,
		IsSelf: true,
	}, nil
}

// ResolveByEmail looks up an identity by email. Returns nil if not found.
func (r *Resolver) ResolveByEmail(email string) (*models.Identity, error) {
	return r.db.GetIdentityByEmail(email)
}

// ResolveBySourceUID looks up an identity by vendor-specific source and user ID.
// Returns nil if not found.
func (r *Resolver) ResolveBySourceUID(source, uid string) (*models.Identity, error) {
	return r.db.GetIdentityBySourceUID(source, uid)
}

// IsSelf returns true if the given email belongs to the self identity.
func (r *Resolver) IsSelf(email string) (bool, error) {
	identity, err := r.db.GetIdentityByEmail(email)
	if err != nil {
		return false, err
	}
	if identity != nil && identity.IsSelf {
		return true, nil
	}
	// Also check identity links (secondary emails).
	identity, err = r.db.GetIdentityBySourceUID("git", email)
	if err != nil {
		return false, err
	}
	return identity != nil && identity.IsSelf, nil
}
