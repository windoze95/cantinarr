package arr

// What to change so a problem stops happening, as opposed to how to repair the
// one in front of you.
//
// The Import Doctor answers "what is wrong with this download" and the agent
// repairs it. Neither ever says "this is the fourth time this month, and fixing
// it again will not help" — and for a good number of these labels, repeating the
// repair genuinely cannot help, because the cause is a setting.
//
// Two deliberate limits:
//
//   - This is advice, never an action. Cantinarr cannot write most of what is
//     named here — it has no client support for release profiles, indexer
//     settings, delay profiles, or the download-client config — and inventing a
//     config change from one week's incidents would be worse than saying
//     nothing. The admin makes the change.
//   - A label with no honest preventative answer gets NO entry, and an absent
//     entry means no notice is ever raised for it. "Waiting to import" and
//     "Already imported" are ordinary states; "Import blocked" and "Download
//     error" are too generic to advise on. Manufacturing advice to fill the
//     table would train an admin to skip the table.

// Prevention scopes name what the change is actually about, because the answer
// is frequently not the *arr the incident was detected on. Two Sonarrs pointed
// at one qBittorrent will each raise their own notice about the same box.
const (
	PreventionScopeInstance = "instance" // the *arr's own settings
	PreventionScopeClient   = "client"   // the download client, possibly shared
	PreventionScopeDisk     = "disk"     // storage, permissions, or the host
)

// Prevention is the server-authored guidance for one problem label. Every field
// is a code constant: none of it is agent-authored, and none of it interpolates
// anything from an *arr, so it is safe as fixed copy anywhere.
type Prevention struct {
	// Problem is the exact arr.Diagnosis.Problem label this advises on.
	Problem string
	// Scope says which system the admin has to go and change.
	Scope string
	// Why explains, in one sentence, why repairing each occurrence again will
	// not stop the next one.
	Why string
	// Steps are the places to look, in the service's own menu vocabulary so an
	// admin can follow them without translation.
	Steps []string
}

// preventionCatalog is keyed by problem label. Order is irrelevant; the map is
// the lookup.
var preventionCatalog = map[string]Prevention{
	ProblemNoFreeSpace: {
		Problem: ProblemNoFreeSpace,
		Scope:   PreventionScopeDisk,
		Why:     "Each repair moves one import along; it does not make room. While the disk stays this full, the next download fails the same way.",
		Steps: []string{
			"Check free space on the root folder's drive, in Settings > Media Management.",
			"Confirm your download client is removing completed downloads once they import, rather than seeding them forever in the same place.",
			"Settings > Media Management > Importing has a Minimum Free Space threshold — raising it fails a grab early instead of half-importing it.",
		},
	},
	ProblemRemotePathMapping: {
		Problem: ProblemRemotePathMapping,
		Scope:   PreventionScopeInstance,
		Why:     "The download client is reporting a path this service cannot see. Every download it finishes will report the same unusable path until the mapping is corrected.",
		Steps: []string{
			"Settings > Download Clients > Remote Path Mappings — add or fix the mapping from the client's path to the path this service sees.",
			"If both run in containers, check that the same volume is mounted at the same path in each.",
		},
	},
	ProblemPathPermissions: {
		Problem: ProblemPathPermissions,
		Scope:   PreventionScopeDisk,
		Why:     "The service can see the file and is not allowed to move it. That is a property of the folder, not of any one download.",
		Steps: []string{
			"Check ownership and permissions on both the download folder and the library folder.",
			"If these run in containers, confirm they share the same user and group ids, and that the umask leaves new files writable.",
		},
	},
	ProblemPathNotAccessible: {
		Problem: ProblemPathNotAccessible,
		Scope:   PreventionScopeDisk,
		Why:     "The path the service was told to use does not exist from where it is running, so nothing that lands there can ever be imported.",
		Steps: []string{
			"Confirm the root folder in Settings > Media Management still exists and is mounted.",
			"Settings > Download Clients > Remote Path Mappings — check the client's completed-download path resolves for this service too.",
		},
	},
	ProblemClientUnreachable: {
		Problem: ProblemClientUnreachable,
		Scope:   PreventionScopeClient,
		Why:     "Nothing in the queue can progress while the service cannot reach the download client, and that is a connection between two systems rather than a problem with any release.",
		Steps: []string{
			"Settings > Download Clients — check the host, port, and credentials.",
			"If the client runs behind a VPN container, confirm the service can still reach it: a VPN's firewall commonly blocks traffic from the local network by default.",
			"If several services share this client, this same problem is affecting all of them.",
		},
	},
	ProblemClientError: {
		Problem: ProblemClientError,
		Scope:   PreventionScopeClient,
		Why:     "The download client itself is reporting the failure, so the service is only relaying it — a different release will fail the same way.",
		Steps: []string{
			"Open the download client directly and check its own error log, disk space, and session state.",
			"Settings > Download Clients — confirm its category and completed-download folder are still valid.",
		},
	},
	ProblemMagnetUnresolvable: {
		Problem: ProblemMagnetUnresolvable,
		Scope:   PreventionScopeClient,
		Why:     "The client cannot turn a magnet link into a torrent, which is a networking capability rather than anything about the release it was handed.",
		Steps: []string{
			"Confirm the client can reach trackers and DHT — a VPN without port forwarding is the usual cause.",
			"Settings > Indexers — an indexer that serves .torrent files rather than magnets avoids the problem entirely.",
			"Settings > Profiles > Delay Profiles — preferring Usenet where you have it sidesteps this class of failure.",
		},
	},
	ProblemUnextractedArchive: {
		Problem: ProblemUnextractedArchive,
		Scope:   PreventionScopeClient,
		Why:     "Nothing in this stack unpacks archives on its own, so every packed release will wait for a manual hand-off until something does.",
		Steps: []string{
			"Run an unpacker such as Unpackerr alongside the download client, and let it watch that category.",
			"Settings > Download Clients — check the category this service uses matches the one the unpacker watches.",
		},
	},
	ProblemDownloadStalled: {
		Problem: ProblemDownloadStalled,
		Scope:   PreventionScopeInstance,
		Why:     "Blocklisting a dead torrent and grabbing the next one works, but it is a coin flip each time while the releases being offered have no health behind them.",
		Steps: []string{
			"Settings > Indexers — raise the minimum seeders on the torrent indexers this keeps coming from.",
			"Settings > Profiles > Delay Profiles — preferring Usenet where you have it removes the seeder problem rather than managing it.",
			"If it is always the same indexer, consider dropping it.",
		},
	},
	ProblemDangerousFile: {
		Problem: ProblemDangerousFile,
		Scope:   PreventionScopeInstance,
		Why:     "An indexer that has offered malware once is not a per-release problem; the next release from the same source deserves the same suspicion.",
		Steps: []string{
			"Settings > Indexers — check which indexer these came from and consider removing it.",
			"If you keep it, lower its priority so healthier indexers are tried first.",
		},
	},
	ProblemSample: {
		Problem: ProblemSample,
		Scope:   PreventionScopeInstance,
		Why:     "Repeatedly grabbing sample clips means an indexer is listing them as full releases, which no amount of per-download repair changes.",
		Steps: []string{
			"Settings > Indexers — identify the source and lower its priority, or remove it.",
			"Settings > Profiles — a minimum size on the quality profile rejects a sample before it is ever grabbed.",
			// The one cause in this catalog Cantinarr can already change — through
			// the assistant's existing previewed, one-turn, restorable apply flow.
			// Deliberately a pointer rather than a second apply mechanism: the
			// profile write's one-use handoff depends on authenticated in-app
			// chat-turn provenance, and that boundary is not forked for a notice.
			"Or ask the in-app AI assistant to raise that profile's minimum size — it previews the exact change and applies it in the same turn, recorded in Configuration history with a one-time restore.",
		},
	},
	ProblemPreAirSeasonFill: {
		Problem: ProblemPreAirSeasonFill,
		Scope:   PreventionScopeInstance,
		Why:     "Files keep arriving for episodes that have not been broadcast yet, which means an indexer is publishing fakes ahead of air and the automatic search is taking them.",
		Steps: []string{
			"Settings > Indexers — check which indexer these came from; this is what its listings look like, not a one-off.",
			"Raise that indexer's minimum age so a release has to survive a while before it can be grabbed automatically.",
			"A release profile that rejects the offending release group stops the same source coming back under a new title.",
		},
	},
}

// PreventionFor returns the guidance for a problem label, if there is any
// honest guidance to give.
// PreventionLiveSection maps the problem labels whose named settings the
// server can now READ (via each client's secret-free GetConfigSummary) to
// their config section. The advice Steps stay fixed code constants — the
// live values join the notice's measurement side, never its instructions —
// and a label absent here simply keeps its advice-only notice.
func PreventionLiveSection(problem string) (string, bool) {
	switch problem {
	case ProblemRemotePathMapping:
		return ConfigRemotePathMappings, true
	case ProblemDownloadStalled:
		return ConfigIndexers, true
	case ProblemClientUnreachable, ProblemClientError:
		return ConfigDownloadClients, true
	}
	return "", false
}

// PreventionProblems lists every problem label the catalog advises on.
func PreventionProblems() []string {
	out := make([]string, 0, len(preventionCatalog))
	for problem := range preventionCatalog {
		out = append(out, problem)
	}
	return out
}

func PreventionFor(problem string) (Prevention, bool) {
	p, ok := preventionCatalog[problem]
	return p, ok
}
