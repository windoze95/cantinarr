package serversettings

import "testing"

func TestValidateURL(t *testing.T) {
	valid := []string{
		"", // empty clears the setting
		"http://tower.local/Docker",
		"https://portainer.example.com",
		"http://192.168.1.10:9000/#/containers",
	}
	for _, u := range valid {
		if err := validateURL("management_url", u); err != nil {
			t.Errorf("validateURL(%q) = %v, want nil", u, err)
		}
	}

	invalid := []string{
		"tower.local/Docker", // no scheme
		"ftp://host/path",    // wrong scheme
		"http://",            // no host
		"just some text",
	}
	for _, u := range invalid {
		if err := validateURL("management_url", u); err == nil {
			t.Errorf("validateURL(%q) = nil, want error", u)
		}
	}
}

// TestSetExternalURL pins the external-address contract: http(s) only, stored
// without the trailing slash so clients can append API paths verbatim, and
// empty clears it back to "links use the generating app's own address".
func TestSetExternalURL(t *testing.T) {
	s := newTestService(t, false)

	if _, err := s.SetExternalURL("https://cantina.example.com/"); err != nil {
		t.Fatalf("SetExternalURL: %v", err)
	}
	if got := s.Get().ExternalURL; got != "https://cantina.example.com" {
		t.Errorf("ExternalURL = %q, want the trailing slash stripped", got)
	}

	if _, err := s.SetExternalURL("cantina.example.com"); err == nil {
		t.Error("SetExternalURL without a scheme = nil, want error")
	}
	if got := s.Get().ExternalURL; got != "https://cantina.example.com" {
		t.Errorf("ExternalURL = %q after a rejected write, want the stored value untouched", got)
	}

	if _, err := s.SetExternalURL(""); err != nil {
		t.Fatalf("SetExternalURL clear: %v", err)
	}
	if got := s.Get().ExternalURL; got != "" {
		t.Errorf("ExternalURL = %q after clearing, want empty", got)
	}
}
