import '../../../core/models/library_sort.dart';
import '../../../core/utils/library_sort.dart';
import '../data/chaptarr_models.dart';

List<ChaptarrAuthor> sortChaptarrLibrary(Iterable<ChaptarrAuthor> authors,
    LibrarySortSelection selection, LibrarySortLabels labels) =>
  sortLibrary(authors, selection, id: (a) => a.id,
    name: (a) => librarySortName(a.sortName, a.authorName),
    value: (a, field) => switch (field) {
      LibrarySortField.alphabetical => librarySortName(a.sortName, a.authorName),
      LibrarySortField.lastName => librarySortName(a.sortNameLastFirst,
          a.authorNameLastFirst ?? ''),
      LibrarySortField.audiobookMetadataProfile => labels.name(
          LibrarySortLookup.metadataProfiles, a.audiobookMetadataProfileId),
      LibrarySortField.audiobookQualityProfile => labels.name(
          LibrarySortLookup.qualityProfiles, a.audiobookQualityProfileId),
      LibrarySortField.bookCompletion => a.statistics == null ? null :
          LibraryCompletion(a.statistics!.availableBookCount, a.statistics!.bookCount),
      LibrarySortField.books => a.statistics?.bookCount,
      LibrarySortField.added => a.added,
      LibrarySortField.ebookMetadataProfile => labels.name(
          LibrarySortLookup.metadataProfiles, a.ebookMetadataProfileId),
      LibrarySortField.ebookQualityProfile => labels.name(
          LibrarySortLookup.qualityProfiles, a.ebookQualityProfileId),
      LibrarySortField.lastBook => a.lastBookRelease,
      LibrarySortField.nextBook => a.nextBookRelease,
      LibrarySortField.path => a.path,
      LibrarySortField.size => a.statistics?.sizeOnDisk,
      LibrarySortField.monitoredStatus => (a.monitored ? 2 : 0) +
          (a.status == 'continuing' ? 1 : 0),
      _ => throw ArgumentError.value(field, 'field', 'Not a Chaptarr sort'),
    });
