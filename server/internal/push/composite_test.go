package push

import "testing"

// recordingNotifier records the events it receives.
type recordingNotifier struct {
	userEvents  []string
	adminEvents []string
}

func (r *recordingNotifier) NotifyUser(userID int64, eventType string, data map[string]interface{}) {
	r.userEvents = append(r.userEvents, eventType)
}

func (r *recordingNotifier) NotifyAdmins(eventType string, data map[string]interface{}) {
	r.adminEvents = append(r.adminEvents, eventType)
}

func TestCompositeFansOut(t *testing.T) {
	a := &recordingNotifier{}
	b := &recordingNotifier{}
	c := NewComposite(a, b)

	c.NotifyUser(1, "request_decision", nil)
	c.NotifyAdmins("request_pending", nil)

	for _, n := range []*recordingNotifier{a, b} {
		if len(n.userEvents) != 1 || n.userEvents[0] != "request_decision" {
			t.Errorf("user events = %v, want [request_decision]", n.userEvents)
		}
		if len(n.adminEvents) != 1 || n.adminEvents[0] != "request_pending" {
			t.Errorf("admin events = %v, want [request_pending]", n.adminEvents)
		}
	}
}

func TestCompositeSkipsNil(t *testing.T) {
	a := &recordingNotifier{}
	c := NewComposite(a, nil)

	// Must not panic on the nil member.
	c.NotifyUser(1, "request_decision", nil)
	if len(a.userEvents) != 1 {
		t.Errorf("user events = %v, want one", a.userEvents)
	}
}
