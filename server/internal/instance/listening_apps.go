package instance

import "fmt"

// ListeningApps names supported clients, never arbitrary launch URLs. Empty
// values mean browser in instance defaults and inheritance in user overrides.
type ListeningApps struct {
	IOS     string `json:"ios"`
	Android string `json:"android"`
}

func (a ListeningApps) Validate() error {
	if a.IOS != "" && a.IOS != "browser" && a.IOS != "audiobookshelf" && a.IOS != "shelfplayer" {
		return fmt.Errorf("unsupported iPhone or iPad listening app")
	}
	if a.Android != "" && a.Android != "browser" && a.Android != "audiobookshelf" && a.Android != "theshelf" {
		return fmt.Errorf("unsupported Android listening app")
	}
	return nil
}

// WithDefaults resolves each platform independently. An explicit browser
// choice overrides an administrator's app just like any other personal choice.
func (a ListeningApps) WithDefaults(defaults *ListeningApps) ListeningApps {
	if defaults != nil {
		if a.IOS == "" {
			a.IOS = defaults.IOS
		}
		if a.Android == "" {
			a.Android = defaults.Android
		}
	}
	if a.IOS == "" {
		a.IOS = "browser"
	}
	if a.Android == "" {
		a.Android = "browser"
	}
	return a
}
