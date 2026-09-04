// arr_lidarr.go — fake Lidarr v1 behind /api/instances/{id}/api/v1/*.
//
// SKELETON: answers 404 for everything. The music domain replaces this file
// wholesale; handleLidarrProxy's signature is what instances.go dispatches to.
package main

import "net/http"

// handleLidarrProxy serves the fake Lidarr. rest is the path after
// /api/instances/{id}/ (e.g. "api/v1/artist"); the caller has already
// enforced the non-admin allowlist.
func handleLidarrProxy(w http.ResponseWriter, r *http.Request, inst *DemoInstance, isAdmin bool, rest string) {
	writeErr(w, http.StatusNotFound, "not found")
}
