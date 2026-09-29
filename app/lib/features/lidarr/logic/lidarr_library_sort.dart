import '../../../core/models/library_sort.dart';
import '../../../core/utils/library_sort.dart';
import '../data/lidarr_models.dart';

List<LidarrArtist> sortLidarrLibrary(Iterable<LidarrArtist> artists,
    LibrarySortSelection selection, LibrarySortLabels labels) =>
  sortLibrary(artists, selection, id: (a) => a.id,
    name: (a) => librarySortName(a.sortName, a.artistName),
    value: (a, field) => switch (field) {
      LibrarySortField.alphabetical => librarySortName(a.sortName, a.artistName),
      LibrarySortField.albums => a.statistics?.albumCount,
      LibrarySortField.added => a.added,
      LibrarySortField.lastAlbum => a.lastAlbumRelease,
      LibrarySortField.metadataProfile => labels.name(
          LibrarySortLookup.metadataProfiles, a.metadataProfileId),
      LibrarySortField.monitoredStatus => (a.monitored ? 2 : 0) +
          (a.status == 'continuing' ? 1 : 0),
      LibrarySortField.nextAlbum => a.nextAlbumRelease,
      LibrarySortField.path => a.path,
      LibrarySortField.qualityProfile => labels.name(
          LibrarySortLookup.qualityProfiles, a.qualityProfileId),
      LibrarySortField.size => a.statistics?.sizeOnDisk,
      LibrarySortField.tags => labels.tags(a.tags),
      LibrarySortField.trackCompletion => a.statistics == null ? null :
          LibraryCompletion(a.statistics!.trackFileCount, a.statistics!.trackCount),
      LibrarySortField.trackCount => a.statistics?.totalTrackCount,
      LibrarySortField.type => a.artistType,
      _ => throw ArgumentError.value(field, 'field', 'Not a Lidarr sort'),
    });
