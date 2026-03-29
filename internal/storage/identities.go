package storage

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/pavelpiliak/devrecall/pkg/models"
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
