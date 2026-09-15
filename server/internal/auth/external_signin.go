package auth

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Completion tickets carry no session credentials. Both providers bind the
// single-use handoff to the initiating client's private S256 verifier.
func newCompletionTicket() (code, key string, expires time.Time, err error) {
	code, err = randomURLToken(32)
	return code, hashToken(code), time.Now().Add(time.Minute), err
}
func validCompletionProof(verifier, flow, expectedFlow, challenge string, expires time.Time) bool {
	return len(verifier) >= 43 && len(verifier) <= 128 && flow == expectedFlow && time.Now().Before(expires) && verifyPKCES256(verifier, challenge)
}

// issueExternalSession always creates a new device. Reusing a hardware ID must
// never upgrade the provenance of a local device or any of its refresh tokens.
func (s *Service) issueExternalSession(tx *sql.Tx, user *User, name, hardware, method, issuer string, plexID int64) (*TokenResponse, error) {
	response := &TokenResponse{User: userWithPermissions(user), DeviceID: uuid.NewString()}
	if name == "" {
		name = "Plex sign-in"
		if method == "oidc" {
			name = "Single sign-on"
		}
	}
	if _, err := tx.Exec("INSERT INTO devices(id,user_id,device_name,hardware_id,auth_method,oidc_issuer,plex_account_id) VALUES (?,?,?,?,?,?,?)", response.DeviceID, user.ID, name, hardware, method, issuer, plexID); err != nil {
		return nil, ErrAuthUnavailable
	}
	var err error
	response.AccessToken, err = s.signAccessToken(user, response.DeviceID)
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	response.RefreshToken, err = newOpaqueRefreshToken()
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	if _, err = tx.Exec("INSERT INTO refresh_tokens(token_hash,device_id,user_id,expires_at) VALUES (?,?,?,?)", hashToken(response.RefreshToken), response.DeviceID, user.ID, refreshNeverExpires); err != nil {
		return nil, ErrAuthUnavailable
	}
	return response, nil
}
