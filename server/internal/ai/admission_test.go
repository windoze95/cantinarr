package ai

import "testing"

func TestChatAdmissionRejectsConcurrentActor(t *testing.T) {
	a := newChatAdmission()
	release, result := a.tryAcquire(7, aiSourcePersonal)
	if result != chatAdmitted {
		t.Fatalf("first admission = %v", result)
	}
	if secondRelease, result := a.tryAcquire(7, aiSourceShared); result != chatActorBusy || secondRelease != nil {
		t.Fatalf("second admission release_nil=%t result=%v, want actor busy", secondRelease == nil, result)
	}
	release()
	if releaseAgain, result := a.tryAcquire(7, aiSourceShared); result != chatAdmitted {
		t.Fatalf("admission after release = %v", result)
	} else {
		releaseAgain()
	}
}

func TestChatAdmissionBoundsSharedProviderCalls(t *testing.T) {
	a := newChatAdmission()
	releases := make([]func(), 0, maxConcurrentSharedAIChats)
	for i := 0; i < maxConcurrentSharedAIChats; i++ {
		release, result := a.tryAcquire(int64(i+1), aiSourceShared)
		if result != chatAdmitted {
			t.Fatalf("shared admission %d = %v", i, result)
		}
		releases = append(releases, release)
	}
	if release, result := a.tryAcquire(999, aiSourceShared); result != chatCapacityBusy || release != nil {
		t.Fatalf("excess shared admission release_nil=%t result=%v, want capacity busy", release == nil, result)
	}
	// Personal calls retain their separate provider budget while shared is full.
	personalRelease, result := a.tryAcquire(1000, aiSourcePersonal)
	if result != chatAdmitted {
		t.Fatalf("personal admission while shared full = %v", result)
	}
	personalRelease()
	for _, release := range releases {
		release()
	}
}

func TestAutonomousAdmissionUsesDistinctSerializedSystemActor(t *testing.T) {
	a := newChatAdmission()
	systemRelease, result := a.tryAcquireKey(remediationAdmissionActor, aiSourceShared)
	if result != chatAdmitted {
		t.Fatalf("system admission = %v", result)
	}
	if release, result := a.tryAcquireKey(remediationAdmissionActor, aiSourceShared); result != chatActorBusy || release != nil {
		t.Fatalf("second system admission release_nil=%t result=%v", release == nil, result)
	}
	// The namespace cannot collide with any positive Cantinarr user ID.
	userRelease, result := a.tryAcquire(1, aiSourceShared)
	if result != chatAdmitted {
		t.Fatalf("user admission while system active = %v", result)
	}
	userRelease()
	systemRelease()
}
