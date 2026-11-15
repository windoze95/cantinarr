package audiobookshelf

import (
	"context"
	"errors"
	"slices"
)

// TagAccessPolicy is the live ABS item-tag visibility setting for one account.
// It is read during migration preflight so a pre-existing custom restriction
// is never silently overwritten.
type TagAccessPolicy struct {
	IsAdministrator bool
	AccessAllTags   bool
	DenySelected    bool
	SelectedTags    []string
}

func (c *Client) TagAccessPolicy(ctx context.Context, id string) (TagAccessPolicy, error) {
	u, err := c.getUser(ctx, id)
	if err != nil {
		return TagAccessPolicy{}, err
	}
	p := u.Permissions
	if p.AccessAllTags == nil || p.SelectedTagsDenied == nil || u.Tags == nil {
		return TagAccessPolicy{}, errors.New("audiobookshelf returned incomplete tag access policy")
	}
	return TagAccessPolicy{
		IsAdministrator: u.remote().IsAdministrator,
		AccessAllTags:   *p.AccessAllTags,
		DenySelected:    *p.SelectedTagsDenied,
		SelectedTags:    slices.Clone(u.Tags),
	}, nil
}
