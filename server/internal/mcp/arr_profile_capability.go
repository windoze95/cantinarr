package mcp

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

const (
	profileChangeReferencePrefix = "profile_change_"
	profileChangeTTL             = 15 * time.Minute
	profileChangeRandomBytes     = 32
	maxPendingProfileChanges     = 512
)

type profileChangeProposal struct {
	Reference          string
	UserID             int64
	DeviceID           string
	IssuedTurnID       string
	Service            string
	InstanceID         string
	InstanceBinding    instance.ArrSettingsFingerprint
	ProfileID          int
	ProfileName        string
	ProfileHash        [32]byte
	DesiredProfileHash [32]byte
	CustomFormatHash   [32]byte
	LanguageHash       [32]byte
	HasLanguageHash    bool
	Plan               profileChangePlan
	Diff               []string
	IssuedAt           time.Time
	ExpiresAt          time.Time
}

type profileChangeTarget struct {
	UserID     int64
	DeviceID   string
	Service    string
	InstanceID string
	ProfileID  int
}

// profileChangeStore is an in-memory, one-shot capability store. Restarting
// Cantinarr intentionally invalidates every pending proposal. References are
// random capabilities, but apply additionally requires the same authenticated
// user, device, origin, and issuing chat turn. The identifier never becomes a
// later-message confirmation mechanism.
type profileChangeStore struct {
	mu        sync.Mutex
	proposals map[string]profileChangeProposal
	targets   map[profileChangeTarget]string
	now       func() time.Time
	random    io.Reader
}

func newProfileChangeStore() *profileChangeStore {
	return &profileChangeStore{
		proposals: make(map[string]profileChangeProposal),
		targets:   make(map[profileChangeTarget]string),
		now:       time.Now,
		random:    rand.Reader,
	}
}

func (s *profileChangeStore) save(proposal profileChangeProposal) (profileChangeProposal, error) {
	if s == nil || proposal.UserID <= 0 || strings.TrimSpace(proposal.DeviceID) == "" ||
		strings.TrimSpace(proposal.IssuedTurnID) == "" || proposal.Service == "" ||
		proposal.InstanceID == "" || proposal.ProfileID <= 0 {
		return profileChangeProposal{}, fmt.Errorf("profile change proposal is not bound to an interactive actor and target")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.cleanupExpiredLocked(now)
	var reference string
	for attempts := 0; attempts < 4; attempts++ {
		candidate, err := newProfileChangeReference(s.random)
		if err != nil {
			return profileChangeProposal{}, fmt.Errorf("generate profile change reference: %w", err)
		}
		if _, collision := s.proposals[candidate]; !collision {
			reference = candidate
			break
		}
	}
	if reference == "" {
		return profileChangeProposal{}, fmt.Errorf("could not allocate a unique profile change reference")
	}

	target := proposal.target()
	previous := s.targets[target]
	if previous != "" {
		delete(s.proposals, previous)
	} else if len(s.proposals) >= maxPendingProfileChanges {
		s.evictOldestLocked()
	}

	proposal.Reference = reference
	proposal.IssuedAt = now
	proposal.ExpiresAt = now.Add(profileChangeTTL)
	proposal.Diff = append([]string(nil), proposal.Diff...)
	proposal.Plan = proposal.Plan.clone()
	s.proposals[reference] = proposal
	s.targets[target] = reference
	return proposal, nil
}

// claimAutonomous consumes a preview only inside the same authenticated chat
// turn that created it. The admin's explicit natural-language request is the
// authority; the model may preview and apply without asking the person to copy
// a capability string back into chat. A later turn cannot replay the reference.
func (s *profileChangeStore) claimAutonomous(reference string, userID int64, deviceID, turnID string, origin CallOrigin) (profileChangeProposal, bool) {
	if s == nil || !isProfileChangeReference(reference) {
		return profileChangeProposal{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupExpiredLocked(s.now())
	proposal, ok := s.proposals[reference]
	if !ok || proposal.UserID != userID || proposal.DeviceID != deviceID ||
		origin != OriginInteractiveChat || turnID == "" || turnID != proposal.IssuedTurnID {
		return profileChangeProposal{}, false
	}
	delete(s.proposals, reference)
	if s.targets[proposal.target()] == reference {
		delete(s.targets, proposal.target())
	}
	proposal.Diff = append([]string(nil), proposal.Diff...)
	proposal.Plan = proposal.Plan.clone()
	return proposal, true
}

func (s *profileChangeStore) evictOldestLocked() {
	var oldestReference string
	var oldest time.Time
	for reference, proposal := range s.proposals {
		if oldestReference == "" || proposal.IssuedAt.Before(oldest) ||
			(proposal.IssuedAt.Equal(oldest) && reference < oldestReference) {
			oldestReference = reference
			oldest = proposal.IssuedAt
		}
	}
	if oldestReference == "" {
		return
	}
	proposal := s.proposals[oldestReference]
	delete(s.proposals, oldestReference)
	if s.targets[proposal.target()] == oldestReference {
		delete(s.targets, proposal.target())
	}
}

func (s *profileChangeStore) cleanupExpiredLocked(now time.Time) {
	for reference, proposal := range s.proposals {
		if now.Before(proposal.ExpiresAt) {
			continue
		}
		delete(s.proposals, reference)
		if s.targets[proposal.target()] == reference {
			delete(s.targets, proposal.target())
		}
	}
}

func (p profileChangeProposal) target() profileChangeTarget {
	return profileChangeTarget{
		UserID: p.UserID, DeviceID: p.DeviceID, Service: p.Service,
		InstanceID: p.InstanceID, ProfileID: p.ProfileID,
	}
}

func newProfileChangeReference(random io.Reader) (string, error) {
	buffer := make([]byte, profileChangeRandomBytes)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", err
	}
	return profileChangeReferencePrefix + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func isProfileChangeReference(reference string) bool {
	if !strings.HasPrefix(reference, profileChangeReferencePrefix) {
		return false
	}
	encoded := strings.TrimPrefix(reference, profileChangeReferencePrefix)
	if len(encoded) != base64.RawURLEncoding.EncodedLen(profileChangeRandomBytes) {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	return err == nil && len(decoded) == profileChangeRandomBytes
}
