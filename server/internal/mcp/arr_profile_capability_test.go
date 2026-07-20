package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type profileCapabilityErrorReader struct {
	err error
}

func (r profileCapabilityErrorReader) Read([]byte) (int, error) {
	return 0, r.err
}

type profileCapabilityCounterReader struct {
	next uint64
}

func (r *profileCapabilityCounterReader) Read(buffer []byte) (int, error) {
	for i := range buffer {
		buffer[i] = 0xa5
	}
	if len(buffer) >= 8 {
		binary.BigEndian.PutUint64(buffer[len(buffer)-8:], r.next)
	}
	r.next++
	return len(buffer), nil
}

func profileCapabilityProposal(profileID int) profileChangeProposal {
	upgradeAllowed := true
	return profileChangeProposal{
		UserID:       41,
		DeviceID:     "device-41",
		IssuedTurnID: "turn-preview",
		Service:      "radarr",
		InstanceID:   "radarr-main",
		ProfileID:    profileID,
		ProfileName:  "HD-1080p",
		Plan: profileChangePlan{
			UpgradeAllowed: &upgradeAllowed,
			CustomFormatScores: []resolvedProfileFormatScore{
				{FormatID: 7, FormatName: "Not English", Score: -10000},
			},
		},
		Diff: []string{"upgradeAllowed: false -> true"},
	}
}

func saveProfileCapability(t *testing.T, store *profileChangeStore, proposal profileChangeProposal) profileChangeProposal {
	t.Helper()
	saved, err := store.save(proposal)
	if err != nil {
		t.Fatalf("save profile change proposal: %v", err)
	}
	return saved
}

func TestNewProfileChangeReferenceHasExpectedEntropyAndShape(t *testing.T) {
	if profileChangeRandomBytes != 32 {
		t.Fatalf("reference entropy = %d bytes, want 32", profileChangeRandomBytes)
	}

	randomBytes := make([]byte, profileChangeRandomBytes)
	for i := range randomBytes {
		randomBytes[i] = byte(i)
	}
	reference, err := newProfileChangeReference(bytes.NewReader(randomBytes))
	if err != nil {
		t.Fatalf("newProfileChangeReference: %v", err)
	}
	if !strings.HasPrefix(reference, profileChangeReferencePrefix) {
		t.Fatalf("reference = %q, want prefix %q", reference, profileChangeReferencePrefix)
	}
	encoded := strings.TrimPrefix(reference, profileChangeReferencePrefix)
	if got, want := len(encoded), base64.RawURLEncoding.EncodedLen(profileChangeRandomBytes); got != want {
		t.Fatalf("encoded reference length = %d, want %d", got, want)
	}
	if strings.ContainsAny(encoded, "+/=") {
		t.Fatalf("reference is not unpadded URL-safe base64: %q", reference)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode reference: %v", err)
	}
	if !bytes.Equal(decoded, randomBytes) {
		t.Fatalf("decoded reference = %x, want %x", decoded, randomBytes)
	}
	if !isProfileChangeReference(reference) {
		t.Fatalf("generated reference was rejected: %q", reference)
	}

	standardBase64 := profileChangeReferencePrefix + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{0xff}, profileChangeRandomBytes))
	for name, malformed := range map[string]string{
		"missing prefix":       encoded,
		"wrong prefix":         "PROFILE_CHANGE_" + encoded,
		"short payload":        profileChangeReferencePrefix + encoded[:len(encoded)-1],
		"long payload":         profileChangeReferencePrefix + encoded + "a",
		"padded payload":       reference + "=",
		"standard base64":      standardBase64,
		"invalid encoded byte": profileChangeReferencePrefix + "!" + encoded[1:],
		"trailing whitespace":  reference + " ",
	} {
		t.Run(name, func(t *testing.T) {
			if isProfileChangeReference(malformed) {
				t.Fatalf("malformed reference was accepted: %q", malformed)
			}
		})
	}
}

func TestNewProfileChangeReferenceAndSaveFailClosedOnRandomFailure(t *testing.T) {
	randomErr := errors.New("random source unavailable")
	if _, err := newProfileChangeReference(profileCapabilityErrorReader{err: randomErr}); !errors.Is(err, randomErr) {
		t.Fatalf("newProfileChangeReference error = %v, want %v", err, randomErr)
	}
	if _, err := newProfileChangeReference(strings.NewReader("short")); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short random read error = %v, want %v", err, io.ErrUnexpectedEOF)
	}

	store := newProfileChangeStore()
	store.random = &profileCapabilityCounterReader{next: 1}
	original := saveProfileCapability(t, store, profileCapabilityProposal(1))
	store.random = profileCapabilityErrorReader{err: randomErr}
	if _, err := store.save(profileCapabilityProposal(1)); !errors.Is(err, randomErr) {
		t.Fatalf("save error = %v, want wrapped random error", err)
	}
	if got := store.targets[original.target()]; got != original.Reference {
		t.Fatalf("failed save replaced existing target reference: got %q, want %q", got, original.Reference)
	}
	if _, ok := store.proposals[original.Reference]; !ok {
		t.Fatal("failed save consumed the existing proposal")
	}
}

func TestProfileChangeStoreTTLBoundary(t *testing.T) {
	issuedAt := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)

	t.Run("valid immediately before expiry", func(t *testing.T) {
		now := issuedAt
		store := newProfileChangeStore()
		store.now = func() time.Time { return now }
		saved := saveProfileCapability(t, store, profileCapabilityProposal(1))
		if !saved.IssuedAt.Equal(issuedAt) {
			t.Fatalf("issued at = %s, want %s", saved.IssuedAt, issuedAt)
		}
		if want := issuedAt.Add(profileChangeTTL); !saved.ExpiresAt.Equal(want) {
			t.Fatalf("expires at = %s, want %s", saved.ExpiresAt, want)
		}

		now = saved.ExpiresAt.Add(-time.Nanosecond)
		if _, ok := store.claim(saved.Reference, saved.UserID, saved.DeviceID, "turn-apply", "APPLY "+saved.Reference, OriginInteractiveChat); !ok {
			t.Fatal("proposal was rejected immediately before expiry")
		}
	})

	t.Run("expired at exact boundary", func(t *testing.T) {
		now := issuedAt
		store := newProfileChangeStore()
		store.now = func() time.Time { return now }
		saved := saveProfileCapability(t, store, profileCapabilityProposal(1))

		now = saved.ExpiresAt
		if _, ok := store.claim(saved.Reference, saved.UserID, saved.DeviceID, "turn-apply", "APPLY "+saved.Reference, OriginInteractiveChat); ok {
			t.Fatal("proposal was accepted at its exact expiry boundary")
		}
		if _, exists := store.proposals[saved.Reference]; exists {
			t.Fatal("expired proposal remained in the proposal index")
		}
		if _, exists := store.targets[saved.target()]; exists {
			t.Fatal("expired proposal remained in the target index")
		}
	})
}

func TestProfileChangeStoreRequiresExactTrustedLaterTurn(t *testing.T) {
	store := newProfileChangeStore()
	saved := saveProfileCapability(t, store, profileCapabilityProposal(1))
	exactConfirmation := "APPLY " + saved.Reference

	for name, attempt := range map[string]struct {
		turnID string
		text   string
	}{
		"missing turn id":     {turnID: "", text: exactConfirmation},
		"preview turn reused": {turnID: saved.IssuedTurnID, text: exactConfirmation},
		"missing apply":       {turnID: "turn-apply", text: saved.Reference},
		"lowercase apply":     {turnID: "turn-apply", text: "apply " + saved.Reference},
		"leading whitespace":  {turnID: "turn-apply", text: " " + exactConfirmation},
		"trailing whitespace": {turnID: "turn-apply", text: exactConfirmation + " "},
		"trailing newline":    {turnID: "turn-apply", text: exactConfirmation + "\n"},
		"extra words":         {turnID: "turn-apply", text: exactConfirmation + " now"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := store.claim(saved.Reference, saved.UserID, saved.DeviceID, attempt.turnID, attempt.text, OriginInteractiveChat); ok {
				t.Fatal("inexact or same-turn confirmation was accepted")
			}
		})
	}

	if _, ok := store.claim(saved.Reference, saved.UserID, saved.DeviceID, "turn-apply", exactConfirmation, OriginInteractiveChat); !ok {
		t.Fatal("exact trusted confirmation on a different turn was rejected after non-consuming refusals")
	}
}

func TestProfileChangeStoreActorDeviceAndOriginRefusalsDoNotConsume(t *testing.T) {
	store := newProfileChangeStore()
	saved := saveProfileCapability(t, store, profileCapabilityProposal(1))
	confirmation := "APPLY " + saved.Reference

	for name, attempt := range map[string]struct {
		userID   int64
		deviceID string
		origin   CallOrigin
	}{
		"anonymous actor":  {userID: 0, deviceID: saved.DeviceID, origin: OriginInteractiveChat},
		"different actor":  {userID: saved.UserID + 1, deviceID: saved.DeviceID, origin: OriginInteractiveChat},
		"missing device":   {userID: saved.UserID, deviceID: "", origin: OriginInteractiveChat},
		"different device": {userID: saved.UserID, deviceID: saved.DeviceID + "-other", origin: OriginInteractiveChat},
		"external mcp":     {userID: saved.UserID, deviceID: saved.DeviceID, origin: OriginExternalMCP},
		"missing origin":   {userID: saved.UserID, deviceID: saved.DeviceID, origin: ""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := store.claim(saved.Reference, attempt.userID, attempt.deviceID, "turn-apply", confirmation, attempt.origin); ok {
				t.Fatal("wrong actor, device, or origin was accepted")
			}
		})
	}

	claimed, ok := store.claim(saved.Reference, saved.UserID, saved.DeviceID, "turn-apply", confirmation, OriginInteractiveChat)
	if !ok || claimed.Reference != saved.Reference {
		t.Fatalf("valid actor claim after refusals = (%q, %t), want %q", claimed.Reference, ok, saved.Reference)
	}
}

func TestProfileChangeStoreClaimIsOneShot(t *testing.T) {
	store := newProfileChangeStore()
	saved := saveProfileCapability(t, store, profileCapabilityProposal(1))
	claim := func() (profileChangeProposal, bool) {
		return store.claim(saved.Reference, saved.UserID, saved.DeviceID, "turn-apply", "APPLY "+saved.Reference, OriginInteractiveChat)
	}

	claimed, ok := claim()
	if !ok || claimed.Reference != saved.Reference {
		t.Fatalf("first claim = (%q, %t), want %q", claimed.Reference, ok, saved.Reference)
	}
	if _, ok := claim(); ok {
		t.Fatal("proposal was replayable after its first successful claim")
	}
	if _, exists := store.proposals[saved.Reference]; exists {
		t.Fatal("claimed proposal remained in the proposal index")
	}
	if _, exists := store.targets[saved.target()]; exists {
		t.Fatal("claimed proposal remained in the target index")
	}
}

func TestProfileChangeStoreSupersedesSameActorTarget(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	store := newProfileChangeStore()
	store.random = &profileCapabilityCounterReader{next: 1}
	store.now = func() time.Time { return now }
	first := saveProfileCapability(t, store, profileCapabilityProposal(1))

	now = now.Add(time.Second)
	secondProposal := profileCapabilityProposal(1)
	secondProposal.Diff = []string{"cutoff: 4 -> 7"}
	second := saveProfileCapability(t, store, secondProposal)
	if first.Reference == second.Reference {
		t.Fatalf("superseding save reused reference %q", first.Reference)
	}
	if len(store.proposals) != 1 || len(store.targets) != 1 {
		t.Fatalf("store sizes after supersession = proposals %d targets %d, want 1 and 1", len(store.proposals), len(store.targets))
	}
	if _, exists := store.proposals[first.Reference]; exists {
		t.Fatal("superseded proposal remained in the proposal index")
	}
	if got := store.targets[second.target()]; got != second.Reference {
		t.Fatalf("target index = %q, want newest reference %q", got, second.Reference)
	}
	if _, ok := store.claim(first.Reference, first.UserID, first.DeviceID, "turn-apply", "APPLY "+first.Reference, OriginInteractiveChat); ok {
		t.Fatal("superseded proposal could still be claimed")
	}
	if _, ok := store.claim(second.Reference, second.UserID, second.DeviceID, "turn-apply", "APPLY "+second.Reference, OriginInteractiveChat); !ok {
		t.Fatal("newest proposal could not be claimed")
	}
}

func TestProfileChangeStoreSupersessionScopeIncludesActorAndTarget(t *testing.T) {
	mutations := map[string]func(*profileChangeProposal){
		"user": func(proposal *profileChangeProposal) {
			proposal.UserID++
		},
		"device": func(proposal *profileChangeProposal) {
			proposal.DeviceID += "-other"
		},
		"service": func(proposal *profileChangeProposal) {
			proposal.Service = "sonarr"
		},
		"instance": func(proposal *profileChangeProposal) {
			proposal.InstanceID = "radarr-secondary"
		},
		"profile": func(proposal *profileChangeProposal) {
			proposal.ProfileID++
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			store := newProfileChangeStore()
			store.random = &profileCapabilityCounterReader{next: 1}
			first := saveProfileCapability(t, store, profileCapabilityProposal(1))
			secondProposal := profileCapabilityProposal(1)
			mutate(&secondProposal)
			second := saveProfileCapability(t, store, secondProposal)

			if len(store.proposals) != 2 || len(store.targets) != 2 {
				t.Fatalf("distinct scope was superseded: proposals %d targets %d", len(store.proposals), len(store.targets))
			}
			if _, exists := store.proposals[first.Reference]; !exists {
				t.Fatal("first distinct proposal was removed")
			}
			if _, exists := store.proposals[second.Reference]; !exists {
				t.Fatal("second distinct proposal was not stored")
			}
		})
	}
}

func TestProfileChangeStoreEvictsOldestAtGlobalCap(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	store := newProfileChangeStore()
	store.random = &profileCapabilityCounterReader{next: 1}
	store.now = func() time.Time { return now }

	saved := make([]profileChangeProposal, 0, maxPendingProfileChanges+1)
	for profileID := 1; profileID <= maxPendingProfileChanges+1; profileID++ {
		saved = append(saved, saveProfileCapability(t, store, profileCapabilityProposal(profileID)))
		now = now.Add(time.Millisecond)
	}

	if got := len(store.proposals); got != maxPendingProfileChanges {
		t.Fatalf("proposal count = %d, want cap %d", got, maxPendingProfileChanges)
	}
	if got := len(store.targets); got != maxPendingProfileChanges {
		t.Fatalf("target count = %d, want cap %d", got, maxPendingProfileChanges)
	}
	oldest := saved[0]
	if _, exists := store.proposals[oldest.Reference]; exists {
		t.Fatalf("oldest proposal %q was not evicted", oldest.Reference)
	}
	if _, exists := store.targets[oldest.target()]; exists {
		t.Fatal("oldest proposal remained in the target index after eviction")
	}
	for _, retained := range []profileChangeProposal{saved[1], saved[len(saved)-1]} {
		if _, exists := store.proposals[retained.Reference]; !exists {
			t.Fatalf("newer proposal %q was unexpectedly evicted", retained.Reference)
		}
		if got := store.targets[retained.target()]; got != retained.Reference {
			t.Fatalf("target index for %q = %q", retained.Reference, got)
		}
	}
}

func TestProfileChangeStoreConcurrentClaimHasExactlyOneWinner(t *testing.T) {
	store := newProfileChangeStore()
	saved := saveProfileCapability(t, store, profileCapabilityProposal(1))
	confirmation := "APPLY " + saved.Reference

	const claimers = 64
	start := make(chan struct{})
	var ready sync.WaitGroup
	var finished sync.WaitGroup
	var successes atomic.Int32
	ready.Add(claimers)
	finished.Add(claimers)
	for range claimers {
		go func() {
			defer finished.Done()
			ready.Done()
			<-start
			if claimed, ok := store.claim(saved.Reference, saved.UserID, saved.DeviceID, "turn-apply", confirmation, OriginInteractiveChat); ok {
				if claimed.Reference != saved.Reference {
					t.Errorf("claimed reference = %q, want %q", claimed.Reference, saved.Reference)
				}
				successes.Add(1)
			}
		}()
	}
	ready.Wait()
	close(start)
	finished.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("successful concurrent claims = %d, want exactly 1", got)
	}
	if _, exists := store.proposals[saved.Reference]; exists {
		t.Fatal("concurrently claimed proposal remained stored")
	}
}
