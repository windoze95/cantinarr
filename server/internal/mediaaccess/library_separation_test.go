package mediaaccess

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/windoze95/cantinarr-server/internal/audiobookshelf"
	"github.com/windoze95/cantinarr-server/internal/instance"
	"github.com/windoze95/cantinarr-server/internal/mediaserver"
)

type separationFakeProvider struct {
	*fakeProvider
	libraries []mediaserver.Library
	items     map[string][]audiobookshelf.BookInventoryItem
	tags      map[string][]string
	policies  map[string]audiobookshelf.TagAccessPolicy
	allowlist map[string][]string
}

func (p *separationFakeProvider) Libraries(context.Context) ([]mediaserver.Library, error) {
	return slices.Clone(p.libraries), nil
}
func (p *separationFakeProvider) BookInventory(_ context.Context, libraryID string) ([]audiobookshelf.BookInventoryItem, error) {
	return append([]audiobookshelf.BookInventoryItem(nil), p.items[libraryID]...), nil
}
func (p *separationFakeProvider) SetItemTag(_ context.Context, itemID, tag string, present bool) error {
	current := slices.Clone(p.tags[itemID])
	if present && !slices.Contains(current, tag) {
		current = append(current, tag)
	}
	if !present {
		filtered := current[:0]
		for _, value := range current {
			if value != tag {
				filtered = append(filtered, value)
			}
		}
		current = filtered
	}
	p.tags[itemID] = current
	return nil
}
func (p *separationFakeProvider) SetItemTagAllowlist(_ context.Context, remoteID string, tags []string) error {
	p.allowlist[remoteID] = slices.Clone(tags)
	policy := p.policies[remoteID]
	policy.AccessAllTags = false
	policy.DenySelected = false
	policy.SelectedTags = slices.Clone(tags)
	p.policies[remoteID] = policy
	return nil
}
func (p *separationFakeProvider) TagAccessPolicy(_ context.Context, remoteID string) (audiobookshelf.TagAccessPolicy, error) {
	return p.policies[remoteID], nil
}

func TestABSLibrarySeparationPreviewApplyAndManagedAccountPolicy(t *testing.T) {
	e := newEnv(t)
	userID := e.user("reader")
	instanceID := e.mediaServer("audiobookshelf", "Books", instance.MediaServerConfig{})
	provider := &separationFakeProvider{
		fakeProvider: e.provider,
		libraries:    []mediaserver.Library{{ID: "books", Name: "Books", CollectionType: "book"}},
		items: map[string][]audiobookshelf.BookInventoryItem{
			"books": {{ID: "item-1", LibraryID: "books", Tags: []string{"Classics"}, HasEbook: true}},
		},
		tags:      map[string][]string{"item-1": {"Classics"}},
		policies:  map[string]audiobookshelf.TagAccessPolicy{},
		allowlist: map[string][]string{},
	}
	e.providers[instanceID] = provider
	e.grantType(userID, "audiobookshelf", instanceID)
	remoteID := provider.addUser("reader", false, false)
	if inserted, err := e.svc.insertAccount(accountRow{UserID: userID, InstanceID: instanceID, RemoteUserID: remoteID, RemoteUsername: "reader", CreatedByCantinarr: true, ManageAccess: true}, true); err != nil || !inserted {
		t.Fatalf("insert managed account = %v, %v", inserted, err)
	}
	provider.policies[remoteID] = audiobookshelf.TagAccessPolicy{AccessAllTags: true, SelectedTags: []string{}}

	preview, err := e.svc.PreviewABSLibrarySeparation(context.Background(), instanceID, []string{"books"}, userID)
	if err != nil || preview.State != "previewed" || !preview.CanApply || preview.Libraries[0].SharedCount != 1 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if err = e.svc.ApplyABSLibrarySeparation(context.Background(), instanceID, "stale", true); !errors.Is(err, ErrSeparationPlan) {
		t.Fatalf("stale plan error = %v", err)
	}
	if len(provider.tags["item-1"]) != 1 || !slices.Contains(provider.tags["item-1"], "Classics") {
		t.Fatalf("stale plan mutated item tags: %v", provider.tags["item-1"])
	}
	if err = e.svc.ApplyABSLibrarySeparation(context.Background(), instanceID, preview.PlanHash, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(provider.tags["item-1"], absSharedLibraryTag) {
		t.Fatalf("shared item missing shared tag: %v", provider.tags["item-1"])
	}
	wantAccess := []string{absSharedLibraryTag, ABSUserTag(userID)}
	slices.Sort(wantAccess)
	if !reflect.DeepEqual(provider.allowlist[remoteID], wantAccess) {
		t.Fatalf("user allowlist = %v, want %v", provider.allowlist[remoteID], wantAccess)
	}
	status, err := e.svc.ABSLibrarySeparationStatus(instanceID)
	if err != nil || status.State != "enabled" || status.Libraries[0].State != "enabled" {
		t.Fatalf("status = %+v, %v", status, err)
	}
}

func TestABSLibrarySeparationRequiresCompleteBookLibraryPreview(t *testing.T) {
	e := newEnv(t)
	instanceID := e.mediaServer("audiobookshelf", "Books", instance.MediaServerConfig{})
	e.providers[instanceID] = &separationFakeProvider{
		fakeProvider: e.provider,
		libraries: []mediaserver.Library{
			{ID: "books", Name: "Books", CollectionType: "book"},
			{ID: "audio", Name: "Audio", CollectionType: "book"},
		},
		items: map[string][]audiobookshelf.BookInventoryItem{}, tags: map[string][]string{},
		policies: map[string]audiobookshelf.TagAccessPolicy{}, allowlist: map[string][]string{},
	}
	_, err := e.svc.PreviewABSLibrarySeparation(context.Background(), instanceID, []string{"books"}, 1)
	if !errors.Is(err, ErrSeparationLibrary) {
		t.Fatalf("partial preview error = %v", err)
	}
}

func TestABSLibrarySeparationRejectsInventoryChangedAfterPreview(t *testing.T) {
	e := newEnv(t)
	adminID := e.user("reviewer")
	instanceID := e.mediaServer("audiobookshelf", "Books", instance.MediaServerConfig{})
	provider := &separationFakeProvider{
		fakeProvider: e.provider,
		libraries:    []mediaserver.Library{{ID: "books", Name: "Books", CollectionType: "book"}},
		items: map[string][]audiobookshelf.BookInventoryItem{
			"books": {{ID: "item-1", LibraryID: "books", ASIN: "0123456789", HasEbook: true}},
		},
		tags: map[string][]string{}, policies: map[string]audiobookshelf.TagAccessPolicy{}, allowlist: map[string][]string{},
	}
	e.providers[instanceID] = provider
	preview, err := e.svc.PreviewABSLibrarySeparation(context.Background(), instanceID, []string{"books"}, adminID)
	if err != nil {
		t.Fatal(err)
	}
	provider.items["books"][0].ASIN = "9876543210"
	if err = e.svc.ApplyABSLibrarySeparation(context.Background(), instanceID, preview.PlanHash, true); !errors.Is(err, ErrSeparationPlan) {
		t.Fatalf("changed inventory should invalidate preview, got %v", err)
	}
	if len(provider.tags["item-1"]) != 0 || len(provider.allowlist) != 0 {
		t.Fatalf("stale preview mutated access: tags=%v allowlist=%v", provider.tags, provider.allowlist)
	}
}

func TestABSSeparationPreflightDoesNotOverrideCustomTagPolicy(t *testing.T) {
	e := newEnv(t)
	userID := e.user("reader")
	instanceID := e.mediaServer("audiobookshelf", "Books", instance.MediaServerConfig{})
	provider := &separationFakeProvider{
		fakeProvider: e.provider,
		libraries:    []mediaserver.Library{{ID: "books", Name: "Books", CollectionType: "book"}},
		items:        map[string][]audiobookshelf.BookInventoryItem{"books": {{ID: "item-1", LibraryID: "books", Tags: []string{}, HasEbook: true}}},
		tags:         map[string][]string{},
		policies:     map[string]audiobookshelf.TagAccessPolicy{},
		allowlist:    map[string][]string{},
	}
	e.providers[instanceID] = provider
	e.grantType(userID, "audiobookshelf", instanceID)
	remoteID := provider.addUser("reader", false, false)
	if inserted, err := e.svc.insertAccount(accountRow{UserID: userID, InstanceID: instanceID, RemoteUserID: remoteID, RemoteUsername: "reader", CreatedByCantinarr: true, ManageAccess: true}, true); err != nil || !inserted {
		t.Fatalf("insert managed account = %v, %v", inserted, err)
	}
	provider.policies[remoteID] = audiobookshelf.TagAccessPolicy{AccessAllTags: false, DenySelected: false, SelectedTags: []string{"manual-private-tag"}}
	preview, err := e.svc.PreviewABSLibrarySeparation(context.Background(), instanceID, []string{"books"}, userID)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.svc.ApplyABSLibrarySeparation(context.Background(), instanceID, preview.PlanHash, true); !errors.Is(err, ErrSeparationPolicy) {
		t.Fatalf("custom policy should block rollout, got %v", err)
	}
	if len(provider.allowlist) != 0 {
		t.Fatalf("custom policy was overwritten: %+v", provider.allowlist)
	}
}
