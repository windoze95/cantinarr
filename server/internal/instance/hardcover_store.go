package instance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/windoze95/cantinarr-server/internal/hardcover"
)

var errHardcoverChanged = errors.New("Hardcover connection changed; reload the instance and try again")
var errHardcoverMissing = errors.New("Chaptarr instance not found")

// HardcoverConnectionState is safe metadata, with a revision for conditional
// writes. It neither decrypts credentials nor renews an expired access token.
type HardcoverConnectionState struct {
	Supported      bool   `json:"supported"`
	Configured     bool   `json:"configured"`
	OAuthAvailable bool   `json:"oauth_available"`
	Method         string `json:"method"`
	Reconnect      bool   `json:"reconnect_required"`
	ConnectionID   string `json:"connection_id,omitempty"`
	Revision       int64  `json:"revision"`
}

func (s *Store) HardcoverState(id string) (HardcoverConnectionState, error) {
	var state HardcoverConnectionState
	var service string
	var tokenPresent bool
	err := s.db.QueryRow(`SELECT i.service_type, i.hardcover_token != '', i.hardcover_revision,
  COALESCE(l.connection_id, ''), COALESCE(c.reconnect, 0)
  FROM service_instances i LEFT JOIN hardcover_instance_connections l ON l.instance_id=i.id
  LEFT JOIN hardcover_connections c ON c.id=l.connection_id WHERE i.id=?`, id).
		Scan(&service, &tokenPresent, &state.Revision, &state.ConnectionID, &state.Reconnect)
	if err == sql.ErrNoRows {
		return state, errHardcoverMissing
	}
	if err != nil {
		return state, err
	}
	state.Supported = SupportsHardcover(service)
	state.OAuthAvailable = state.Supported && s.cipher != nil
	state.Method = "none"
	if state.ConnectionID != "" {
		state.Method = "oauth"
	} else if tokenPresent {
		state.Method = "api_token"
	}
	state.Configured = state.Supported && state.Method != "none"
	return state, nil
}

// beginHardcoverChange reserves a newer admin intent before any provider I/O.
// Reserving never disconnects the working credential.
func (s *Store) beginHardcoverChange(id string) (int64, error) {
	var revision int64
	err := s.db.QueryRow(`UPDATE service_instances SET hardcover_revision=hardcover_revision+1
  WHERE id=? AND service_type='chaptarr' RETURNING hardcover_revision`, id).Scan(&revision)
	if err == sql.ErrNoRows {
		return 0, errHardcoverMissing
	}
	return revision, err
}

func cleanupHardcoverConnections(tx *sql.Tx) error {
	_, err := tx.Exec(`DELETE FROM hardcover_connections WHERE NOT EXISTS
  (SELECT 1 FROM hardcover_instance_connections l WHERE l.connection_id=hardcover_connections.id)`)
	return err
}

func (s *Store) setHardcoverTokenAtRevision(id, token string, revision int64) error {
	encrypted, err := s.cipher.Encrypt(token)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE service_instances SET hardcover_token=?, hardcover_revision=hardcover_revision+1
  WHERE id=? AND service_type='chaptarr' AND hardcover_revision=?`, encrypted, id, revision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errHardcoverChanged
	}
	if _, err = tx.Exec("DELETE FROM hardcover_instance_connections WHERE instance_id=?", id); err != nil {
		return err
	}
	if err = cleanupHardcoverConnections(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) connectHardcoverOAuth(id string, revision int64, tokens hardcover.OAuthTokens) (string, error) {
	raw, err := json.Marshal(tokens)
	if err != nil {
		return "", err
	}
	encrypted, err := s.cipher.Encrypt(string(raw))
	if err != nil {
		return "", err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE service_instances SET hardcover_token='', hardcover_revision=hardcover_revision+1
  WHERE id=? AND service_type='chaptarr' AND hardcover_revision=?`, id, revision)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return "", errHardcoverChanged
	}
	connectionID := uuid.NewString()
	if _, err = tx.Exec("INSERT INTO hardcover_connections(id,credentials) VALUES(?,?)", connectionID, encrypted); err != nil {
		return "", err
	}
	if _, err = tx.Exec(`INSERT INTO hardcover_instance_connections(instance_id,connection_id) VALUES(?,?)
  ON CONFLICT(instance_id) DO UPDATE SET connection_id=excluded.connection_id`, id, connectionID); err != nil {
		return "", err
	}
	if err = cleanupHardcoverConnections(tx); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return connectionID, nil
}

type hardcoverCredential struct {
	Tokens    hardcover.OAuthTokens
	Revision  int64
	Reconnect bool
}

func (s *Store) hardcoverCredential(id string) (hardcoverCredential, error) {
	var c hardcoverCredential
	var raw string
	err := s.db.QueryRow("SELECT credentials,revision,reconnect FROM hardcover_connections WHERE id=?", id).Scan(&raw, &c.Revision, &c.Reconnect)
	if err != nil {
		return c, err
	}
	raw, err = s.cipher.Decrypt(raw)
	if err != nil {
		return c, err
	}
	if json.Unmarshal([]byte(raw), &c.Tokens) != nil || c.Tokens.AccessToken == "" {
		return c, errors.New("unreadable Hardcover credential")
	}
	return c, nil
}

// updateHardcoverCredential uses a revision comparison even though the manager
// serializes local refreshes. A stale writer cannot recreate deleted tokens.
func (s *Store) updateHardcoverCredential(id string, c hardcoverCredential) error {
	raw, err := json.Marshal(c.Tokens)
	if err != nil {
		return err
	}
	encrypted, err := s.cipher.Encrypt(string(raw))
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE hardcover_connections SET credentials=?,reconnect=?,revision=revision+1 WHERE id=? AND revision=?`, encrypted, c.Reconnect, id, c.Revision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errHardcoverChanged
	}
	return nil
}

type HardcoverApplyTarget struct {
	InstanceID string `json:"instance_id"`
	Revision   int64  `json:"revision"`
}

type HardcoverApplyResult struct {
	InstanceID string `json:"instance_id"`
	Applied    bool   `json:"applied"`
	Error      string `json:"error,omitempty"`
}

// applyHardcoverConnection rechecks the source and target inside each write
// transaction. An explicit target revision protects edits made while the admin
// was reading the Apply to all dialog. A missing target never blocks siblings.
func (s *Store) applyHardcoverConnection(ctx context.Context, source, connectionID string, target HardcoverApplyTarget) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	err = tx.QueryRow(`SELECT l.connection_id FROM hardcover_instance_connections l
  JOIN service_instances i ON i.id=l.instance_id JOIN hardcover_connections c ON c.id=l.connection_id
  WHERE i.id=? AND i.service_type='chaptarr' AND c.reconnect=0`, source).Scan(&current)
	if err == sql.ErrNoRows || (err == nil && current != connectionID) {
		return errHardcoverChanged
	}
	if err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE service_instances SET hardcover_token='',hardcover_revision=hardcover_revision+1
  WHERE id=? AND service_type='chaptarr' AND hardcover_revision=?`, target.InstanceID, target.Revision)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errHardcoverChanged
	}
	if _, err = tx.Exec(`INSERT INTO hardcover_instance_connections(instance_id,connection_id) VALUES(?,?)
  ON CONFLICT(instance_id) DO UPDATE SET connection_id=excluded.connection_id`, target.InstanceID, connectionID); err != nil {
		return err
	}
	if err = cleanupHardcoverConnections(tx); err != nil {
		return err
	}
	return tx.Commit()
}
