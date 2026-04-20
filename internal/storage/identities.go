package storage

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

// InsertIdentity creates a new identity. Returns the new ID.
// If an identity with the same email already exists, returns its ID instead.
func (db *DB) InsertIdentity(name, email string, isSelf bool) (int64, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	selfInt := 0
	if isSelf {
		selfInt = 1
	}

	res, err := db.Exec(
		`INSERT INTO identities (name, email, is_self) VALUES (?, ?, ?)
		 ON CONFLICT(email) DO UPDATE SET
			name    = CASE WHEN excluded.name != '' THEN excluded.name ELSE identities.name END,
			is_self = MAX(identities.is_self, excluded.is_self)`,
		name, email, selfInt,
	)
	if err != nil {
		return 0, fmt.Errorf("insert identity: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil || id == 0 {
		// ON CONFLICT doesn't set LastInsertId — look it up.
		return db.getIdentityIDByEmail(email)
	}
	return id, nil
}

func (db *DB) getIdentityIDByEmail(email string) (int64, error) {
	var id int64
	err := db.QueryRow("SELECT id FROM identities WHERE email = ?", email).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("get identity by email: %w", err)
	}
	return id, nil
}

// GetIdentityByEmail returns the identity with the given email, or nil if not found.
func (db *DB) GetIdentityByEmail(email string) (*models.Identity, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var i models.Identity
	var isSelf int
	err := db.QueryRow(
		"SELECT id, name, email, is_self FROM identities WHERE email = ?", email,
	).Scan(&i.ID, &i.Name, &i.Email, &isSelf)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get identity: %w", err)
	}
	i.IsSelf = isSelf == 1
	return &i, nil
}

// GetSelfIdentity returns the identity marked as self, or nil if not set up yet.
func (db *DB) GetSelfIdentity() (*models.Identity, error) {
	var i models.Identity
	err := db.QueryRow(
		"SELECT id, name, email, is_self FROM identities WHERE is_self = 1 LIMIT 1",
	).Scan(&i.ID, &i.Name, &i.Email, &i.IsSelf)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get self identity: %w", err)
	}
	return &i, nil
}

// ListIdentities returns all identities.
func (db *DB) ListIdentities() ([]models.Identity, error) {
	rows, err := db.Query("SELECT id, name, email, is_self FROM identities ORDER BY is_self DESC, name ASC")
	if err != nil {
		return nil, fmt.Errorf("list identities: %w", err)
	}
	defer rows.Close()

	var result []models.Identity
	for rows.Next() {
		var i models.Identity
		var isSelf int
		if err := rows.Scan(&i.ID, &i.Name, &i.Email, &isSelf); err != nil {
			return nil, fmt.Errorf("scan identity: %w", err)
		}
		i.IsSelf = isSelf == 1
		result = append(result, i)
	}
	return result, rows.Err()
}

// InsertIdentityLink links a vendor-specific user ID to an identity.
// On conflict (same source+source_uid), updates the identity_id.
func (db *DB) InsertIdentityLink(identityID int64, source, sourceUID string) error {
	_, err := db.Exec(
		`INSERT INTO identity_links (identity_id, source, source_uid) VALUES (?, ?, ?)
		 ON CONFLICT(source, source_uid) DO UPDATE SET identity_id = excluded.identity_id`,
		identityID, source, sourceUID,
	)
	if err != nil {
		return fmt.Errorf("insert identity link: %w", err)
	}
	return nil
}

// GetIdentityBySourceUID returns the identity linked to the given source+uid, or nil.
func (db *DB) GetIdentityBySourceUID(source, sourceUID string) (*models.Identity, error) {
	var i models.Identity
	var isSelf int
	err := db.QueryRow(`
		SELECT i.id, i.name, i.email, i.is_self
		FROM identities i
		JOIN identity_links l ON l.identity_id = i.id
		WHERE l.source = ? AND l.source_uid = ?`,
		source, sourceUID,
	).Scan(&i.ID, &i.Name, &i.Email, &isSelf)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get identity by source uid: %w", err)
	}
	i.IsSelf = isSelf == 1
	return &i, nil
}

// IdentityLink represents a source-specific link to an identity.
type IdentityLink struct {
	ID         int64
	IdentityID int64
	Source     string
	SourceUID  string
}

// ListIdentityLinks returns all links for the given identity.
func (db *DB) ListIdentityLinks(identityID int64) ([]IdentityLink, error) {
	rows, err := db.Query(
		"SELECT id, identity_id, source, source_uid FROM identity_links WHERE identity_id = ? ORDER BY source, source_uid",
		identityID,
	)
	if err != nil {
		return nil, fmt.Errorf("list identity links: %w", err)
	}
	defer rows.Close()

	var result []IdentityLink
	for rows.Next() {
		var l IdentityLink
		if err := rows.Scan(&l.ID, &l.IdentityID, &l.Source, &l.SourceUID); err != nil {
			return nil, fmt.Errorf("scan identity link: %w", err)
		}
		result = append(result, l)
	}
	return result, rows.Err()
}

// GetIdentityByID returns the identity with the given ID, or nil if not found.
func (db *DB) GetIdentityByID(id int64) (*models.Identity, error) {
	var i models.Identity
	var isSelf int
	err := db.QueryRow(
		"SELECT id, name, email, is_self FROM identities WHERE id = ?", id,
	).Scan(&i.ID, &i.Name, &i.Email, &isSelf)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get identity by id: %w", err)
	}
	i.IsSelf = isSelf == 1
	return &i, nil
}

// MergeIdentities merges secondary identities into the primary one.
// All identity_links and activities from secondary identities are reassigned
// to the primary, then the secondary identities are deleted.
func (db *DB) MergeIdentities(primaryID int64, secondaryIDs []int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin merge tx: %w", err)
	}
	defer tx.Rollback()

	for _, secID := range secondaryIDs {
		// Reassign identity_links. Delete on conflict (same source+source_uid already linked to primary).
		if _, err := tx.Exec(
			`UPDATE OR IGNORE identity_links SET identity_id = ? WHERE identity_id = ?`,
			primaryID, secID,
		); err != nil {
			return fmt.Errorf("reassign links from %d: %w", secID, err)
		}
		// Remove any leftover duplicates that couldn't be reassigned.
		if _, err := tx.Exec(
			`DELETE FROM identity_links WHERE identity_id = ?`, secID,
		); err != nil {
			return fmt.Errorf("cleanup links for %d: %w", secID, err)
		}

		// Reassign activities.
		if _, err := tx.Exec(
			`UPDATE activities SET identity_id = ? WHERE identity_id = ?`,
			primaryID, secID,
		); err != nil {
			return fmt.Errorf("reassign activities from %d: %w", secID, err)
		}

		// Delete the secondary identity.
		if _, err := tx.Exec(
			`DELETE FROM identities WHERE id = ?`, secID,
		); err != nil {
			return fmt.Errorf("delete identity %d: %w", secID, err)
		}
	}

	return tx.Commit()
}

// DeleteIdentity removes an identity and its links. Activities are unlinked (identity_id set to NULL).
func (db *DB) DeleteIdentity(id int64) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE activities SET identity_id = NULL WHERE identity_id = ?`, id); err != nil {
		return fmt.Errorf("unlink activities: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM identity_links WHERE identity_id = ?`, id); err != nil {
		return fmt.Errorf("delete links: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM identities WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete identity: %w", err)
	}

	return tx.Commit()
}
