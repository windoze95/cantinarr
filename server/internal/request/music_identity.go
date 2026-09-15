package request

import (
	"database/sql"
	"strings"
)

type identityReader interface {
	Query(string, ...any) (*sql.Rows, error)
}

// Only a verified delivery binding creates an alias edge. A title, search
// result position, or client-supplied mapping can never equate two groups.
func musicIdentityWhere(db identityReader, instanceID, foreignID, provider, sourceID string) (string, []any, error) {
	seed := foreignID
	if provider == "musicbrainz" {
		seed = sourceID
	}
	rows, err := db.Query(`WITH RECURSIVE edges(a,b) AS (
 SELECT r.foreign_id,d.canonical_foreign_id FROM request_log r JOIN request_dispatch d ON d.request_id=r.id
 WHERE r.media_type='music' AND r.instance_id=? AND COALESCE(r.foreign_id,'')!='' AND d.canonical_foreign_id!=''
 UNION SELECT r.catalog_id,d.canonical_foreign_id FROM request_log r JOIN request_dispatch d ON d.request_id=r.id
 WHERE r.media_type='music' AND r.instance_id=? AND r.catalog_provider='musicbrainz' AND d.canonical_foreign_id!=''
 ), aliases(id) AS (
 SELECT ? WHERE ?!=''
 UNION SELECT CASE WHEN e.a=a.id THEN e.b ELSE e.a END FROM edges e JOIN aliases a ON e.a=a.id OR e.b=a.id
 ) SELECT id FROM aliases`, instanceID, instanceID, seed, seed)
	if err != nil {
		return "", nil, err
	}
	ids := []any{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	readErr := rows.Err()
	rows.Close()
	if err != nil {
		return "", nil, err
	}
	if readErr != nil {
		return "", nil, readErr
	}
	clauses := []string{}
	args := []any{}
	if len(ids) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		clauses = append(clauses, `r.foreign_id IN (`+placeholders+`)`, `(r.catalog_provider='musicbrainz' AND r.catalog_id IN (`+placeholders+`))`, `EXISTS(SELECT 1 FROM request_dispatch md WHERE md.request_id=r.id AND md.canonical_foreign_id IN (`+placeholders+`))`)
		for range 3 {
			args = append(args, ids...)
		}
	}
	if provider != "" {
		clauses = append(clauses, `(r.catalog_provider=? AND r.catalog_id=?)`)
		args = append(args, provider, sourceID)
	}
	if len(clauses) == 0 {
		return "0", args, nil
	}
	return "(" + strings.Join(clauses, " OR ") + ")", args, nil
}
