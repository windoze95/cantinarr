package musicdiscovery

import "context"

// Catalog is shared by HTTP discovery and durable request delivery.
type Catalog interface {
	Feed(context.Context, string, string, string, int) ([]byte, error)
	Search(context.Context, string, int, ...bool) ([]byte, error)
	SearchArtists(context.Context, string, int) ([]byte, error)
	Artist(context.Context, string) ([]byte, error)
	ArtistAlbums(context.Context, string, int) ([]byte, error)
	Album(context.Context, string) ([]byte, error)
	Artwork(context.Context, string) ([]byte, error)
	ResolveAlbum(context.Context, string, bool) (Album, error)
}
