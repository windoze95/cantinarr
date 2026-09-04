package downloads

import (
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

// backend is what the unified downloads API needs from one download client.
// Each client gets one adapter file (backend_<type>.go) that owns the whole
// mapping between that client's protocol and the normalized shapes, so a new
// client is one adapter plus a case in backendFor, and nothing else here.
type backend interface {
	// Snapshot builds the normalized queue: the downloads still in progress,
	// whether the queue as a whole is paused, and the global download rate.
	Snapshot() (*QueueView, error)
	// History lists completed downloads, newest first, at most limit of them.
	History(limit int) ([]historyItem, error)
	PauseItem(id string) error
	ResumeItem(id string) error
	// DeleteItem removes a queue item; deleteData also deletes its files where
	// the client supports that (NZBGet does not and ignores the flag).
	DeleteItem(id string, deleteData bool) error
	// PauseAll and ResumeAll act on the queue the user sees: torrent clients
	// only touch incomplete torrents, never seeding ones.
	PauseAll() error
	ResumeAll() error
}

// badItemIDError is an item id the backend cannot use; the handlers answer it
// with 400 rather than treating it as an upstream failure.
type badItemIDError struct{ msg string }

func (e *badItemIDError) Error() string { return e.msg }

// downloadClientTypes is the one list of service types the downloads API
// serves. The websocket poller and the setup checklist read it too.
var downloadClientTypes = []string{"sabnzbd", "qbittorrent", "nzbget", "transmission", "deluge"}

// DownloadClientTypes returns the download-client service types in menu
// order: usenet clients first, then torrent clients.
func DownloadClientTypes() []string {
	out := make([]string, len(downloadClientTypes))
	copy(out, downloadClientTypes)
	return out
}

// IsDownloadClientType reports whether serviceType is a download client.
func IsDownloadClientType(serviceType string) bool {
	for _, t := range downloadClientTypes {
		if t == serviceType {
			return true
		}
	}
	return false
}

// backendFor resolves the adapter for a download-client instance through the
// registry's cached clients. Errors are registry errors (unknown instance,
// undecryptable credentials) or the instance not being a download client.
func backendFor(reg *instance.Registry, inst instance.Instance) (backend, error) {
	switch inst.ServiceType {
	case "sabnzbd":
		client, err := reg.GetSabnzbdClient(inst.ID)
		if err != nil {
			return nil, err
		}
		return sabnzbdBackend{client}, nil
	case "qbittorrent":
		client, err := reg.GetQbittorrentClient(inst.ID)
		if err != nil {
			return nil, err
		}
		return qbittorrentBackend{client}, nil
	case "nzbget":
		client, err := reg.GetNzbgetClient(inst.ID)
		if err != nil {
			return nil, err
		}
		return nzbgetBackend{client}, nil
	case "transmission":
		client, err := reg.GetTransmissionClient(inst.ID)
		if err != nil {
			return nil, err
		}
		return transmissionBackend{client}, nil
	case "deluge":
		client, err := reg.GetDelugeClient(inst.ID)
		if err != nil {
			return nil, err
		}
		return delugeBackend{client}, nil
	}
	return nil, fmt.Errorf("instance %s is not a download client", inst.ID)
}
