package mcp

import (
	"context"
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/auth"
	"github.com/windoze95/cantinarr-server/internal/contentpolicy"
	"github.com/windoze95/cantinarr-server/internal/tmdb"
)

// Kids accounts in the tool server: every discover tool runs its titles
// through the caller's policy before they reach the model or the app's
// carousel, and a title the policy hides is reported as not available,
// never described.

const titleNotAvailableText = "That title is not available for this account."

// callerPolicy reads the caller's policy once per call. Admins and the
// server-owned remediation runner never carry one.
func (s *ToolServer) callerPolicy(callCtx CallContext) (*contentpolicy.Policy, error) {
	if s.contentPolicy == nil || callCtx.TrustedInternal || callCtx.UserID <= 0 || callCtx.Role == auth.RoleAdmin {
		return nil, nil
	}
	return s.contentPolicy.PolicyFor(callCtx.UserID, callCtx.Role)
}

// filterResults keeps the results the policy allows and counts the rest.
// An entry that names no media type takes the tool's own. A lookup that
// fails fails the call: the model is told the tool failed rather than
// handed a list that looks complete.
func (s *ToolServer) filterResults(ctx context.Context, policy *contentpolicy.Policy, results []tmdb.SearchResult, defaultMediaType string) ([]tmdb.SearchResult, int, error) {
	if policy == nil || len(results) == 0 || s.contentPolicy == nil {
		return results, 0, nil
	}
	cands := make([]contentpolicy.Candidate, len(results))
	for i, r := range results {
		mediaType := r.MediaType
		if mediaType == "" {
			mediaType = defaultMediaType
		}
		cands[i] = contentpolicy.Candidate{MediaType: mediaType, TMDBID: r.ID, Adult: r.Adult, GenreIDs: r.GenreIDs}
	}
	keep, err := s.contentPolicy.Filter(ctx, policy, cands)
	if err != nil {
		return nil, 0, fmt.Errorf("content limits could not be applied: %w", err)
	}
	kept := make([]tmdb.SearchResult, 0, len(results))
	hidden := 0
	for i, ok := range keep {
		if ok {
			kept = append(kept, results[i])
		} else {
			hidden++
		}
	}
	return kept, hidden, nil
}

// allowsTitle decides one title for the caller.
func (s *ToolServer) allowsTitle(ctx context.Context, policy *contentpolicy.Policy, c contentpolicy.Candidate) (bool, error) {
	if policy == nil || s.contentPolicy == nil {
		return true, nil
	}
	allowed, err := s.contentPolicy.Allows(ctx, policy, c)
	if err != nil {
		return false, fmt.Errorf("content limits could not be applied: %w", err)
	}
	return allowed, nil
}

// hiddenNote tells the model how many titles the account's limits removed,
// so a short list is explained rather than padded from memory.
func hiddenNote(hidden int) string {
	if hidden <= 0 {
		return ""
	}
	if hidden == 1 {
		return "\n1 title hidden by this account's content limits."
	}
	return fmt.Sprintf("\n%d titles hidden by this account's content limits.", hidden)
}
