package auth

import "database/sql"

// SetGrantAddedObserver installs a post-commit callback for onboarding grants.
func (s *Service) SetGrantAddedObserver(f func(int64, string)) { s.grantAdded = f }
func (s *Service) notifyAutoAssignments(userID int64, ids []string) {
	if s.grantAdded != nil {
		for _, id := range ids {
			s.grantAdded(userID, id)
		}
	}
}

// autoAssignUser is called only when the enclosing transaction inserted a new
// regular account. Sign-in, identity linking and invite reissuance never call it.
func autoAssignUser(tx *sql.Tx, userID int64) ([]string, error) {
	rows, err := tx.Query(`SELECT si.id FROM service_instances si JOIN users u ON u.id=? AND u.role='user'
 WHERE si.auto_add_users=1 AND si.service_type IN ('radarr','sonarr','chaptarr','lidarr') ORDER BY si.sort_order,si.name,si.id`, userID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err = tx.Exec("INSERT INTO user_instance_grants(user_id,instance_id) VALUES (?,?)", userID, id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
