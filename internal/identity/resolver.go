package identity

import (
	"fmt"
	"strings"

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

// LinkSource links a vendor-specific user ID to an existing identity found by email.
// If no identity exists for that email, a new one is created.
// Returns the identity that was linked.
func (r *Resolver) LinkSource(source, sourceUID, email, name string) (*models.Identity, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, fmt.Errorf("email is required for identity linking")
	}

	// Check if this source+uid is already linked.
	existing, err := r.db.GetIdentityBySourceUID(source, sourceUID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	// Look up by email — may match a git identity.
	identity, err := r.db.GetIdentityByEmail(email)
	if err != nil {
		return nil, err
	}

	if identity == nil {
		// Also check if email is a source_uid in identity_links (e.g., git uses email as source_uid).
		identity, err = r.db.GetIdentityBySourceUID("git", email)
		if err != nil {
			return nil, err
		}
	}

	if identity == nil {
		// No existing identity — create one.
		id, err := r.db.InsertIdentity(name, email, false)
		if err != nil {
			return nil, fmt.Errorf("create identity for %s: %w", email, err)
		}
		identity = &models.Identity{ID: id, Name: name, Email: email}
	}

	// Create the link.
	if err := r.db.InsertIdentityLink(identity.ID, source, sourceUID); err != nil {
		return nil, fmt.Errorf("link %s/%s: %w", source, sourceUID, err)
	}

	return identity, nil
}

// AutoLinkSlack links a Slack user ID to an identity by matching the Slack profile email
// against existing identities (typically from Git). This is the core of cross-source
// identity resolution.
func (r *Resolver) AutoLinkSlack(slackUserID, email, name string) (*models.Identity, error) {
	return r.LinkSource("slack", slackUserID, email, name)
}

// ListAll returns all identities with their source links.
func (r *Resolver) ListAll() ([]IdentityWithLinks, error) {
	identities, err := r.db.ListIdentities()
	if err != nil {
		return nil, err
	}

	var result []IdentityWithLinks
	for _, id := range identities {
		links, err := r.db.ListIdentityLinks(id.ID)
		if err != nil {
			return nil, fmt.Errorf("list links for %d: %w", id.ID, err)
		}
		result = append(result, IdentityWithLinks{Identity: id, Links: links})
	}
	return result, nil
}

// MergeIdentities merges secondary identities into the primary one.
func (r *Resolver) MergeIdentities(primaryID int64, secondaryIDs []int64) error {
	// Validate primary exists.
	primary, err := r.db.GetIdentityByID(primaryID)
	if err != nil {
		return err
	}
	if primary == nil {
		return fmt.Errorf("primary identity %d not found", primaryID)
	}

	// Validate all secondary IDs exist and are different from primary.
	for _, id := range secondaryIDs {
		if id == primaryID {
			return fmt.Errorf("cannot merge identity %d into itself", id)
		}
		ident, err := r.db.GetIdentityByID(id)
		if err != nil {
			return err
		}
		if ident == nil {
			return fmt.Errorf("identity %d not found", id)
		}
	}

	return r.db.MergeIdentities(primaryID, secondaryIDs)
}

// DeleteIdentity removes an identity and unlinks its activities.
func (r *Resolver) DeleteIdentity(id int64) error {
	identity, err := r.db.GetIdentityByID(id)
	if err != nil {
		return err
	}
	if identity == nil {
		return fmt.Errorf("identity %d not found", id)
	}
	if identity.IsSelf {
		return fmt.Errorf("cannot delete self identity")
	}
	return r.db.DeleteIdentity(id)
}

// IdentityWithLinks pairs an identity with its source links.
type IdentityWithLinks struct {
	Identity models.Identity
	Links    []storage.IdentityLink
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
