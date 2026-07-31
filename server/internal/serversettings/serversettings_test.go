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
		if err := validateURL(u); err != nil {
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
		if err := validateURL(u); err == nil {
			t.Errorf("validateURL(%q) = nil, want error", u)
		}
	}
}
