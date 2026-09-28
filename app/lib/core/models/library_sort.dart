/// Stable preference keys shared by the four library sort menus.
enum LibrarySortField {
  alphabetical, lastName, added, certification, digitalRelease, imdbRating,
  inCinemas, minimumAvailability, monitoredStatus, originalLanguage,
  originalTitle, path, physicalRelease, popularity, qualityProfile, releaseDate,
  rottenTomatoesRating, runtime, size, studio, tags, tmdbRating, traktRating, year,
  episodeCompletion, episodeCount, latestSeason, network, nextAiring,
  previousAiring, rating, seasons, type, audiobookMetadataProfile,
  audiobookQualityProfile, bookCompletion, books, ebookMetadataProfile,
  ebookQualityProfile, lastBook, nextBook, albums, lastAlbum, metadataProfile,
  nextAlbum, trackCompletion, trackCount;

  String label(String module) => switch (this) {
    alphabetical => module == 'chaptarr' ? 'First Name' : 'Alphabetical',
    lastName => 'Last Name',
    added => 'Date Added',
    certification => 'Certification',
    digitalRelease => 'Digital Release',
    imdbRating => 'IMDb Rating',
    inCinemas => 'In Cinemas',
    minimumAvailability => 'Minimum Availability',
    monitoredStatus => module == 'chaptarr' ? 'Status' : 'Monitored Status',
    originalLanguage => 'Original Language',
    originalTitle => 'Original Title',
    path => 'Path',
    physicalRelease => 'Physical Release',
    popularity => 'Popularity',
    qualityProfile => 'Quality Profile',
    releaseDate => 'Release Date',
    rottenTomatoesRating => 'Rotten Tomatoes Rating',
    runtime => 'Runtime',
    size => 'Size',
    studio => 'Studio',
    tags => 'Tags',
    tmdbRating => 'TMDB Rating',
    traktRating => 'Trakt Rating',
    year => 'Year',
    episodeCompletion => 'Episode Completion',
    episodeCount => 'Episode Count',
    latestSeason => 'Latest Season',
    network => 'Network',
    nextAiring => 'Next Airing',
    previousAiring => 'Previous Airing',
    rating => 'Rating',
    seasons => 'Seasons',
    type => 'Type',
    audiobookMetadataProfile => 'Audiobook Metadata Profile',
    audiobookQualityProfile => 'Audiobook Quality Profile',
    bookCompletion => 'Book Completion',
    books => 'Books',
    ebookMetadataProfile => 'eBook Metadata Profile',
    ebookQualityProfile => 'eBook Quality Profile',
    lastBook => 'Last Book',
    nextBook => 'Next Book',
    albums => 'Albums',
    lastAlbum => 'Last Album',
    metadataProfile => 'Metadata Profile',
    nextAlbum => 'Next Album',
    trackCompletion => 'Track Completion',
    trackCount => 'Track Count',
  };

  LibrarySortLookup? get lookup => switch (this) {
    qualityProfile || audiobookQualityProfile || ebookQualityProfile =>
        LibrarySortLookup.qualityProfiles,
    metadataProfile || audiobookMetadataProfile || ebookMetadataProfile =>
        LibrarySortLookup.metadataProfiles,
    tags => LibrarySortLookup.tags,
    _ => null,
  };
}

enum LibrarySortLookup {
  qualityProfiles('quality profiles'), metadataProfiles('metadata profiles'),
  tags('tags');
  const LibrarySortLookup(this.label);
  final String label;
}

const _moduleFields = {
  'radarr': [
    LibrarySortField.alphabetical, LibrarySortField.certification,
    LibrarySortField.added, LibrarySortField.digitalRelease,
    LibrarySortField.imdbRating, LibrarySortField.inCinemas,
    LibrarySortField.minimumAvailability, LibrarySortField.monitoredStatus,
    LibrarySortField.originalLanguage, LibrarySortField.originalTitle,
    LibrarySortField.path, LibrarySortField.physicalRelease,
    LibrarySortField.popularity, LibrarySortField.qualityProfile,
    LibrarySortField.releaseDate, LibrarySortField.rottenTomatoesRating,
    LibrarySortField.runtime, LibrarySortField.size, LibrarySortField.studio,
    LibrarySortField.tags, LibrarySortField.tmdbRating,
    LibrarySortField.traktRating, LibrarySortField.year,
  ],
  'sonarr': [
    LibrarySortField.alphabetical, LibrarySortField.added,
    LibrarySortField.episodeCompletion, LibrarySortField.episodeCount,
    LibrarySortField.latestSeason, LibrarySortField.monitoredStatus,
    LibrarySortField.network, LibrarySortField.nextAiring,
    LibrarySortField.originalLanguage, LibrarySortField.path,
    LibrarySortField.previousAiring, LibrarySortField.qualityProfile,
    LibrarySortField.rating, LibrarySortField.seasons, LibrarySortField.size,
    LibrarySortField.tags, LibrarySortField.type,
  ],
  'chaptarr': [
    LibrarySortField.alphabetical, LibrarySortField.lastName,
    LibrarySortField.audiobookMetadataProfile,
    LibrarySortField.audiobookQualityProfile, LibrarySortField.bookCompletion,
    LibrarySortField.books, LibrarySortField.added,
    LibrarySortField.ebookMetadataProfile, LibrarySortField.ebookQualityProfile,
    LibrarySortField.lastBook, LibrarySortField.nextBook, LibrarySortField.path,
    LibrarySortField.size, LibrarySortField.monitoredStatus,
  ],
  'lidarr': [
    LibrarySortField.alphabetical, LibrarySortField.albums, LibrarySortField.added,
    LibrarySortField.lastAlbum, LibrarySortField.metadataProfile,
    LibrarySortField.monitoredStatus, LibrarySortField.nextAlbum,
    LibrarySortField.path, LibrarySortField.qualityProfile, LibrarySortField.size,
    LibrarySortField.tags, LibrarySortField.trackCompletion,
    LibrarySortField.trackCount, LibrarySortField.type,
  ],
};

List<LibrarySortField> librarySortFields(String module) {
  final fields = [...(_moduleFields[module] ?? const [LibrarySortField.alphabetical])];
  fields.sort((a, b) {
    if (a == b) return 0;
    if (a == LibrarySortField.alphabetical) return -1;
    if (b == LibrarySortField.alphabetical) return 1;
    return a.label(module).toLowerCase().compareTo(b.label(module).toLowerCase());
  });
  return fields;
}

class LibrarySortSelection {
  final LibrarySortField field;
  final bool ascending;
  const LibrarySortSelection({this.field = LibrarySortField.alphabetical,
    this.ascending = true});

  LibrarySortSelection select(LibrarySortField value) => LibrarySortSelection(
    field: value, ascending: value == field ? !ascending : true);

  LibrarySortSelection validated(String module) =>
      librarySortFields(module).contains(field) ? this : const LibrarySortSelection();

  String get key => '${field.name}:${ascending ? 'asc' : 'desc'}';

  static LibrarySortSelection parse(String module, String? value) {
    for (final field in librarySortFields(module)) {
      if (value == '${field.name}:asc' || value == '${field.name}:desc') {
        return LibrarySortSelection(field: field, ascending: value!.endsWith(':asc'));
      }
    }
    return const LibrarySortSelection();
  }

  @override
  bool operator ==(Object other) => other is LibrarySortSelection &&
      field == other.field && ascending == other.ascending;
  @override
  int get hashCode => Object.hash(field, ascending);
}
