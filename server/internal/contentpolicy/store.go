package contentpolicy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrUserNotFound is returned when the user a policy is written for does
	// not exist.
	ErrUserNotFound = errors.New("user not found")
	// ErrAdminAccount is returned when a policy is written for an admin: an
	// admin manages every title and can never be a kids account.
	ErrAdminAccount = errors.New("admin accounts cannot be kids accounts")
)

// Store reads and writes user_content_policies.
type Store struct {
	db *sql.DB
}

// NewStore wraps the database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const policyColumns = `max_movie_rating, max_tv_rating, rating_region, block_unrated, blocked_movie_genres, blocked_tv_genres`

type policyScanner interface {
	Scan(dest ...any) error
}

func scanPolicy(row policyScanner) (*Policy, error) {
	var (
		p          Policy
		movie, tv  string
		blockUnrtd bool
	)
	if err := row.Scan(&p.MaxMovieRating, &p.MaxTVRating, &p.RatingRegion, &blockUnrtd, &movie, &tv); err != nil {
		return nil, err
	}
	p.BlockUnrated = blockUnrtd
	p.BlockedMovieGenres = decodeGenres(movie)
	p.BlockedTVGenres = decodeGenres(tv)
	return &p, nil
}

// Get returns the user's policy, or nil when the account is unrestricted.
func (s *Store) Get(userID int64) (*Policy, error) {
	p, err := scanPolicy(s.db.QueryRow(`SELECT `+policyColumns+` FROM user_content_policies WHERE user_id = ?`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load content policy: %w", err)
	}
	return p, nil
}

// IsChild reports whether the user has a policy row.
func (s *Store) IsChild(userID int64) (bool, error) {
	var child bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM user_content_policies WHERE user_id = ?)`, userID).Scan(&child); err != nil {
		return false, fmt.Errorf("check content policy: %w", err)
	}
	return child, nil
}

// Set creates or replaces the user's policy, making the account a kids
// account. The role check runs inside the same transaction as the write and
// the role change checks for a policy row inside its own, so an account is
// never both admin and child.
func (s *Store) Set(userID int64, p Policy) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	var role string
	if err := tx.QueryRow(`SELECT role FROM users WHERE id = ?`, userID).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("load user: %w", err)
	}
	if role == roleAdmin {
		return ErrAdminAccount
	}
	if _, err := tx.Exec(`
		INSERT INTO user_content_policies
			(user_id, max_movie_rating, max_tv_rating, rating_region, block_unrated, blocked_movie_genres, blocked_tv_genres, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(user_id) DO UPDATE SET
			max_movie_rating = excluded.max_movie_rating,
			max_tv_rating = excluded.max_tv_rating,
			rating_region = excluded.rating_region,
			block_unrated = excluded.block_unrated,
			blocked_movie_genres = excluded.blocked_movie_genres,
			blocked_tv_genres = excluded.blocked_tv_genres,
			updated_at = CURRENT_TIMESTAMP`,
		userID, p.MaxMovieRating, p.MaxTVRating, p.RatingRegion, p.BlockUnrated,
		encodeGenres(p.BlockedMovieGenres), encodeGenres(p.BlockedTVGenres),
	); err != nil {
		return fmt.Errorf("write content policy: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit content policy: %w", err)
	}
	return nil
}

// Clear turns the kids account off. Clearing an account that has no policy
// is not an error.
func (s *Store) Clear(userID int64) error {
	if _, err := s.db.Exec(`DELETE FROM user_content_policies WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("clear content policy: %w", err)
	}
	return nil
}

// PoliciesFor returns the policies of the given users in one query; users
// without a row are absent from the map.
func (s *Store) PoliciesFor(userIDs []int64) (map[int64]*Policy, error) {
	out := map[int64]*Policy{}
	if len(userIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(userIDs))
	args := make([]any, len(userIDs))
	for i, id := range userIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT user_id, `+policyColumns+` FROM user_content_policies WHERE user_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("load content policies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			userID     int64
			p          Policy
			movie, tv  string
			blockUnrtd bool
		)
		if err := rows.Scan(&userID, &p.MaxMovieRating, &p.MaxTVRating, &p.RatingRegion, &blockUnrtd, &movie, &tv); err != nil {
			return nil, fmt.Errorf("scan content policy: %w", err)
		}
		p.BlockUnrated = blockUnrtd
		p.BlockedMovieGenres = decodeGenres(movie)
		p.BlockedTVGenres = decodeGenres(tv)
		policy := p
		out[userID] = &policy
	}
	return out, rows.Err()
}

// encodeGenres stores a sorted, deduplicated id list as a JSON array.
func encodeGenres(ids []int) string {
	data, err := json.Marshal(normalizeGenreIDs(ids))
	if err != nil {
		return "[]"
	}
	return string(data)
}

// decodeGenres reads a stored JSON array, degrading to "nothing hidden" on a
// value that is not one: a hidden-genre list is an extra, the rating cap is
// the safeguard.
func decodeGenres(raw string) []int {
	var ids []int
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return []int{}
	}
	return normalizeGenreIDs(ids)
}

// normalizeGenreIDs drops non-positive ids and duplicates and sorts the
// rest, so two writes of the same set compare equal. The result is never
// nil, so it encodes as [] rather than null.
func normalizeGenreIDs(ids []int) []int {
	seen := map[int]struct{}{}
	out := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}
