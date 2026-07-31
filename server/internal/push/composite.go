package push

// notifier is the subset of request.Notifier the composite fans out to. It is
// duplicated here (rather than importing request) so the push package stays
// free of a dependency on request, which already depends on this package's
// interface shape.
type notifier interface {
	NotifyUser(userID int64, eventType string, data map[string]interface{})
	NotifyAdmins(eventType string, data map[string]interface{})
}

// Composite fans request events out to several notifiers (e.g. the WebSocket
// hub for live clients and the push notifier for offline devices). nil members
// are skipped, so callers can wire whichever sinks are configured.
type Composite struct {
	targets []notifier
}

// NewComposite builds a Composite from the given notifiers, dropping nil ones.
// Pass an untyped nil for a sink that is not configured; the *Notifier and
// *ws.Hub methods are themselves nil-receiver safe, but a typed-nil interface
// value would still pass the nil check, so prefer untyped nils here.
func NewComposite(targets ...notifier) *Composite {
	live := make([]notifier, 0, len(targets))
	for _, t := range targets {
		if t == nil {
			continue
		}
		live = append(live, t)
	}
	return &Composite{targets: live}
}

// NotifyUser fans the event out to every target.
func (c *Composite) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	for _, t := range c.targets {
		t.NotifyUser(userID, eventType, data)
	}
}

// NotifyAdmins fans the event out to every target.
func (c *Composite) NotifyAdmins(eventType string, data map[string]interface{}) {
	for _, t := range c.targets {
		t.NotifyAdmins(eventType, data)
	}
}
