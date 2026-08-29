package plex

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

// Provider adapts one Plex Media Server's plex.tv shares to the
// media-server contract as an invite server (mediaserver.KindInvite). An
// "account" here is a share: the identity is the invitee's email in its
// canonical spelling, a pending invite is a share nobody has accepted, and a
// share that is gone is absence. Everything goes through plex.tv with the
// owner's token; the server itself is never dialed.
type Provider struct {
	client    *Client
	clientID  string
	token     string
	machineID string
}

// NewProvider builds the provider for one server. clientID is the app-wide
// X-Plex-Client-Identifier the token was minted under; machineID is the
// server's plex.tv machine identifier.
func NewProvider(client *Client, clientID, token, machineID string) *Provider {
	return &Provider{client: client, clientID: clientID, token: token, machineID: machineID}
}

// Kind marks the provider as an invite server.
func (p *Provider) Kind() mediaserver.Kind { return mediaserver.KindInvite }

// SystemInfo reads the server as plex.tv knows it, proving both the token
// and that the linked account owns this machine.
func (p *Provider) SystemInfo(ctx context.Context) (mediaserver.SystemInfo, error) {
	if p.machineID == "" {
		return mediaserver.SystemInfo{}, errors.New("no Plex server selected")
	}
	info, err := p.client.GetServer(ctx, p.clientID, p.token, p.machineID)
	if err != nil {
		return mediaserver.SystemInfo{}, err
	}
	return mediaserver.SystemInfo{ServerName: info.Name, Version: info.Version, ID: info.MachineIdentifier}, nil
}

// Libraries lists the server's sections. IDs are the plex.tv-global section
// ids the sharing API expects, rendered as strings.
func (p *Provider) Libraries(ctx context.Context) ([]mediaserver.Library, error) {
	libs, err := p.client.ListLibraries(ctx, p.clientID, p.token, p.machineID)
	if err != nil {
		return nil, err
	}
	out := make([]mediaserver.Library, 0, len(libs))
	for _, lib := range libs {
		out = append(out, mediaserver.Library{ID: strconv.FormatInt(lib.ID, 10), Name: lib.Title, CollectionType: lib.Type})
	}
	return out, nil
}

// entry is one share or pending invite, looked up by identity.
type entry struct {
	user     mediaserver.RemoteUser
	shareID  int64 // set for a share
	inviteID int64 // set for a pending invite that is not yet a share
}

func (e entry) matches(identity string) bool {
	return strings.EqualFold(e.user.ID, identity) || strings.EqualFold(e.user.Name, identity)
}

// entries lists shares and pending invites for this server, shares first so
// an identity that appears in both reads from the share.
func (p *Provider) entries(ctx context.Context) ([]entry, error) {
	shares, err := p.client.ListShares(ctx, p.clientID, p.token, p.machineID)
	if err != nil {
		return nil, err
	}
	out := make([]entry, 0, len(shares))
	for _, s := range shares {
		out = append(out, entry{shareID: s.ID, user: remoteUser(s.Email, s.Username, !s.Accepted)})
	}
	invites, err := p.client.ListInvites(ctx, p.clientID, p.token)
	if err != nil {
		return nil, err
	}
	for _, inv := range invites {
		if !p.inviteCoversThisServer(inv) {
			continue
		}
		already := false
		for _, e := range out {
			if e.matches(inv.Email) || (inv.Username != "" && e.matches(inv.Username)) {
				already = true
				break
			}
		}
		if already {
			continue
		}
		out = append(out, entry{inviteID: inv.ID, user: remoteUser(inv.Email, inv.Username, true)})
	}
	return out, nil
}

// inviteCoversThisServer keeps only the invites that name this machine. An
// invite that names no server at all is kept: plex.tv's sent list does not
// always say, and an invite this account sent is far more likely ours than
// not.
func (p *Provider) inviteCoversThisServer(inv Invite) bool {
	if len(inv.Machines) == 0 {
		return true
	}
	for _, m := range inv.Machines {
		if m == p.machineID {
			return true
		}
	}
	return false
}

func remoteUser(email, username string, pending bool) mediaserver.RemoteUser {
	id := mediaserver.CanonicalEmail(email)
	if id == "" {
		id = mediaserver.CanonicalEmail(username)
	}
	name := username
	if name == "" {
		name = id
	}
	return mediaserver.RemoteUser{ID: id, Name: name, Pending: pending}
}

// Users lists everyone the server is shared with, pending invites included.
func (p *Provider) Users(ctx context.Context) ([]mediaserver.RemoteUser, error) {
	entries, err := p.entries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]mediaserver.RemoteUser, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.user)
	}
	return out, nil
}

func (p *Provider) find(ctx context.Context, identity string) (entry, error) {
	identity = mediaserver.CanonicalEmail(identity)
	if identity == "" {
		return entry{}, mediaserver.ErrUserNotFound
	}
	entries, err := p.entries(ctx)
	if err != nil {
		return entry{}, err
	}
	for _, e := range entries {
		if e.matches(identity) {
			return e, nil
		}
	}
	return entry{}, mediaserver.ErrUserNotFound
}

// GetUser finds the share or pending invite for an identity.
func (p *Provider) GetUser(ctx context.Context, identity string) (mediaserver.RemoteUser, error) {
	e, err := p.find(ctx, identity)
	if err != nil {
		return mediaserver.RemoteUser{}, err
	}
	return e.user, nil
}

// CreateUser sends the share invite. The password must be empty: Plex
// accounts are the person's own. An address plex.tv reports as already
// shared is answered with that share.
func (p *Provider) CreateUser(ctx context.Context, identity, password string, libraryIDs []string) (mediaserver.RemoteUser, error) {
	if password != "" {
		return mediaserver.RemoteUser{}, errors.New("plex invites carry no password")
	}
	identity = mediaserver.CanonicalEmail(identity)
	if !mediaserver.ValidEmail(identity) {
		return mediaserver.RemoteUser{}, mediaserver.ErrInvalidName
	}
	sectionIDs, err := sectionIDs(libraryIDs)
	if err != nil {
		return mediaserver.RemoteUser{}, err
	}
	err = p.client.InviteEmail(ctx, p.clientID, p.token, p.machineID, identity, sectionIDs)
	switch {
	case errors.Is(err, ErrAlreadyShared):
		existing, findErr := p.find(ctx, identity)
		if findErr == nil {
			return existing.user, nil
		}
		if errors.Is(findErr, mediaserver.ErrUserNotFound) {
			return mediaserver.RemoteUser{}, mediaserver.ErrUserExists
		}
		return mediaserver.RemoteUser{}, findErr
	case err != nil:
		return mediaserver.RemoteUser{}, err
	}
	return remoteUser(identity, "", true), nil
}

// SetLibraries re-scopes an existing share. A pending invite keeps the
// libraries it was sent with (plex.tv fixes them at acceptance), and an
// identity with no share needs nothing.
func (p *Provider) SetLibraries(ctx context.Context, identity string, libraryIDs []string) error {
	e, err := p.find(ctx, identity)
	if errors.Is(err, mediaserver.ErrUserNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if e.shareID == 0 {
		return nil
	}
	sectionIDs, err := sectionIDs(libraryIDs)
	if err != nil {
		return err
	}
	return p.client.UpdateShare(ctx, p.clientID, p.token, p.machineID, e.shareID, sectionIDs)
}

// SetDisabled with disabled=true removes the share or withdraws the pending
// invite; disabled=false does nothing — access comes back as a new invite.
func (p *Provider) SetDisabled(ctx context.Context, identity string, disabled bool) error {
	if !disabled {
		return nil
	}
	e, err := p.find(ctx, identity)
	if err != nil {
		return err
	}
	if e.shareID != 0 {
		return p.client.RemoveShare(ctx, p.clientID, p.token, p.machineID, e.shareID)
	}
	return p.client.CancelInvite(ctx, p.clientID, p.token, e.inviteID)
}

// DeleteUser is SetDisabled(true) that treats an already-gone share as done.
func (p *Provider) DeleteUser(ctx context.Context, identity string) error {
	err := p.SetDisabled(ctx, identity, true)
	if errors.Is(err, mediaserver.ErrUserNotFound) {
		return nil
	}
	return err
}

func sectionIDs(libraryIDs []string) ([]int64, error) {
	out := make([]int64, 0, len(libraryIDs))
	for _, id := range libraryIDs {
		n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("library id %q is not a Plex section id", id)
		}
		out = append(out, n)
	}
	return out, nil
}
