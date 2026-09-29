package request

import (
	"fmt"

	"github.com/windoze95/cantinarr-server/internal/chaptarr"
)

// Only these locally constructed messages may reach requesters and logs. Never
// include an upstream body, path, profile name, or connection error here.
type bookConfigurationError struct {
	format  string
	setting string
}

func (e *bookConfigurationError) Error() string {
	return fmt.Sprintf("Cantinarr could not choose %s for this %s. An administrator can set it on the author in Chaptarr, then try again. Your request is saved.", e.setting, e.format)
}

// Existing authors can intentionally have only one format configured. Preserve
// their choices, and fill only missing settings for formats being added. The
// author update is necessary because a book add can retain the disabled "None"
// metadata profile even while filling other fields.
func existingBookConfig(client *chaptarr.Client, author *chaptarr.Author, formats []string) (bookAddConfig, error) {
	config, _ := bookConfigFromAuthor(author)
	metadata, err := client.GetMetadataProfiles()
	if err != nil {
		return config, fmt.Errorf("load metadata profiles: %w", err)
	}
	var quality []chaptarr.QualityProfile
	var roots []chaptarr.RootFolder
	disabledMetadataIDs := make(map[int]bool)
	for _, profile := range metadata {
		if profile.ProfileType == "0" {
			disabledMetadataIDs[profile.ID] = true
		}
	}
	qualityLoaded, rootsLoaded := false, false
	for _, format := range formats {
		qualityID, metadataID, root := config.forFormat(format)
		originalQuality, originalMetadata, originalRoot := qualityID, metadataID, root
		if qualityID <= 0 {
			if !qualityLoaded {
				quality, err = client.GetQualityProfiles()
				if err != nil {
					return config, fmt.Errorf("load quality profiles: %w", err)
				}
				qualityLoaded = true
			}
			var ok bool
			qualityID, ok = selectBookQualityProfile(quality, format)
			if !ok {
				return config, &bookConfigurationError{format, "a quality profile"}
			}
		}
		// Chaptarr's "None" metadata profile is a real positive ID, but it
		// disables the format just as an unset profile does.
		if disabledMetadataIDs[metadataID] {
			metadataID = 0
		}
		if metadataID <= 0 {
			var ok bool
			metadataID, ok = selectBookMetadataProfile(metadata, format)
			if !ok {
				return config, &bookConfigurationError{format, "a metadata profile"}
			}
		}
		if root == "" {
			if !rootsLoaded {
				roots, err = client.GetRootFolders()
				if err != nil {
					return config, fmt.Errorf("load root folders: %w", err)
				}
				rootsLoaded = true
			}
			selected, ok := selectBookRoot(roots, format)
			if !ok {
				return config, &bookConfigurationError{format, "a download folder"}
			}
			root = selected.Path
		}
		if qualityID != originalQuality || metadataID != originalMetadata || root != originalRoot {
			updated, err := client.EnsureAuthorFormatConfig(author.ID, format, qualityID, metadataID, root, disabledMetadataIDs)
			if err != nil {
				return config, err
			}
			config, _ = bookConfigFromAuthor(updated)
		}
	}
	return config, nil
}
