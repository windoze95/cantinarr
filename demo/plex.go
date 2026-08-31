// plex.go — the simulated plex.tv PIN flow, shared by the two surfaces that
// need one: the admin linking a Plex instance in the instance editor, and a
// granted user signing in with Plex from the media-server access guide.
//
// The old /api/admin/plex/* console is gone. The real server deleted it when
// Plex became an instance like any other (PR #519) — access now lives under
// /api/instances/plex/* and /api/media-servers/*, and this file is only the
// PIN machinery those two call into.
package main

import (
	"fmt"
	"sync"
)

// Seeded Plex identity (fictional account + server).
const (
	plexDemoAccount      = "demoplex"
	plexDemoAccountEmail = "demoplex@example.com"
	plexDemoServer       = "Demo Plex"
)

// plexLibrary is one canned library section of the demo's single server.
type plexLibrary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"collection_type"`
}

// plexDemoLibraries is what the shared-libraries picker serves for the demo's
// Plex server.
var plexDemoLibraries = []plexLibrary{
	{ID: "1", Name: "Movies", Type: "movie"},
	{ID: "2", Name: "TV Shows", Type: "show"},
}

var (
	plexMu sync.Mutex

	// PIN-flow simulation: pin id -> completed poll count. A check answers
	// "not approved yet" for the first two polls (the app polls every 3 s) and
	// approves on the third, so the waiting state is actually visible.
	plexPinPolls        = map[int64]int{}
	plexNextPinID int64 = 481000001
)

// plexPinApproveAfter is how many polls a pin waits before plex.tv "approves"
// it.
const plexPinApproveAfter = 3

// plexBeginPin mints a pin the app polls, returning its id and the code the
// user types at plex.tv, plus the sign-in URL.
func plexBeginPin() (pinID int64, code, url string) {
	plexMu.Lock()
	pinID = plexNextPinID
	plexNextPinID++
	plexPinPolls[pinID] = 0
	plexMu.Unlock()
	code = randomHex(8) // 16 hex chars, matches plex.tv code length
	url = fmt.Sprintf(
		"https://app.plex.tv/auth#?clientID=cantinarr-demo&code=%s&context%%5Bdevice%%5D%%5Bproduct%%5D=Cantinarr",
		code)
	return pinID, code, url
}

// plexPollPin advances a pin one poll. found is false for an id that was never
// minted or has already been consumed — the caller answers "the link expired,
// start again", which is the same thing plex.tv's own expiry looks like.
// An approved pin is consumed, so the approval is read exactly once.
func plexPollPin(pinID int64) (approved, found bool) {
	plexMu.Lock()
	defer plexMu.Unlock()
	polls, ok := plexPinPolls[pinID]
	if !ok {
		return false, false
	}
	polls++
	if polls >= plexPinApproveAfter {
		delete(plexPinPolls, pinID)
		return true, true
	}
	plexPinPolls[pinID] = polls
	return false, true
}

// plexServerChoices is the linked account's owned servers, for the instance
// editor's server picker. The demo account owns exactly one.
func plexServerChoices() []map[string]any {
	return []map[string]any{
		{"name": plexDemoServer, "machine_identifier": plexDemoMachineIdentifier},
	}
}
