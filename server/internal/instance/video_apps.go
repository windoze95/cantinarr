package instance

import "fmt"

// VideoApps names supported iPhone/iPad clients, never arbitrary launch URLs.
// Empty values inherit instance defaults, which use the service's official app.
type VideoApps struct {
	IOS string `json:"ios"`
}

func IsVideoServerType(serviceType string) bool {
	return serviceType == "plex" || serviceType == "jellyfin" || serviceType == "emby"
}

func (a VideoApps) Validate() error {
	if a.IOS != "" && a.IOS != "service" && a.IOS != "infuse" && a.IOS != "browser" {
		return fmt.Errorf("unsupported iPhone or iPad video app")
	}
	return nil
}

func (a VideoApps) WithDefaults(defaults *VideoApps) VideoApps {
	if a.IOS == "" && defaults != nil {
		a.IOS = defaults.IOS
	}
	if a.IOS == "" {
		a.IOS = "service"
	}
	return a
}
