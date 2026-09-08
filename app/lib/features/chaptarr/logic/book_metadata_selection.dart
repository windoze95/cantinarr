import '../data/chaptarr_models.dart';
import 'book_identity.dart';

Set<String> _workKeys(ChaptarrBook book) => bookIdentityKeys(book)
    .where((key) =>
        key.startsWith('gr-work:') ||
        key.startsWith('hc-book:') ||
        key.startsWith('ol-work:'))
    .toSet();

bool _conflictingWorks(ChaptarrBook a, ChaptarrBook b) {
  final left = _workKeys(a);
  final right = _workKeys(b);
  for (final kind in ['gr-work:', 'hc-book:', 'ol-work:']) {
    final ids = left.where((key) => key.startsWith(kind)).toSet();
    final others = right.where((key) => key.startsWith(kind)).toSet();
    if (ids.isNotEmpty &&
        others.isNotEmpty &&
        ids.intersection(others).isEmpty) {
      return true;
    }
  }
  return false;
}

Set<String> _editionKeys(ChaptarrBook book) {
  final ordered = [...book.editions]..sort((a, b) {
      if (a.monitored != b.monitored) return a.monitored ? -1 : 1;
      if (a.manualAdd != b.manualAdd) return a.manualAdd ? -1 : 1;
      return a.id.compareTo(b.id);
    });
  final edition = ordered
          .where((edition) =>
              book.foreignEditionId?.isNotEmpty == true &&
              edition.foreignEditionId == book.foreignEditionId)
          .firstOrNull ??
      ordered.firstOrNull;
  // Reuse typed edition/ISBN normalization without letting another edition
  // in the returned work stand in for the publication the reader selected.
  return bookIdentityKeys(ChaptarrBook(
    id: 0,
    title: '',
    foreignEditionId: book.foreignEditionId,
    goodreadsBookId: book.goodreadsBookId,
    editions: edition == null ? const [] : [edition],
  ));
}

bool _sameEdition(ChaptarrBook a, ChaptarrBook b) =>
    _editionKeys(a).intersection(_editionKeys(b)).isNotEmpty;

Set<String> _editionRecordKeys(ChaptarrEdition edition) =>
    bookIdentityKeys(ChaptarrBook(id: 0, title: '', editions: [edition]));

List<ChaptarrEdition> _enrichEditions(
    ChaptarrBook initial, ChaptarrBook fresh) {
  if (initial.editions.isEmpty) {
    final selected = _editionKeys(initial);
    return fresh.editions
        .where((edition) =>
            selected.intersection(_editionRecordKeys(edition)).isNotEmpty)
        .toList();
  }
  // This is a presentation copy of the selected publication. Do not replace
  // its editions wholesale: sparse responses can erase the date, and adding
  // unrelated editions can change which publication sorts first.
  return initial.editions.map((edition) {
    final keys = _editionRecordKeys(edition);
    final matches = fresh.editions
        .where(
            (other) => keys.intersection(_editionRecordKeys(other)).isNotEmpty)
        .toList();
    if (matches.length != 1) return edition;
    final fields = edition.toJson();
    final update = matches.single.toJson();
    for (final field in [
      'releaseDate',
      'title',
      'format',
      'overview',
      'publisher',
    ]) {
      final value = update[field];
      if (value is String && value.trim().isNotEmpty) fields[field] = value;
    }
    if (matches.single.pageCount > 0) {
      fields['pageCount'] = matches.single.pageCount;
    }
    if (matches.single.images.isNotEmpty) fields['images'] = update['images'];
    for (final field in [
      'asin',
      'isbn13',
      'isbn10',
      'goodreadsEditionId',
      'openLibraryEditionId',
      'hardcoverEditionId',
    ]) {
      if (fields[field] == null || fields[field] == '') {
        fields[field] = update[field];
      }
    }
    return ChaptarrEdition.fromJson(fields);
  }).toList();
}

/// An ID lookup may return a canonical provider alias and separate ebook /
/// audiobook projections. Only explicit work IDs prove an alias. Neither
/// title similarity, result order, nor description length proves identity.
ChaptarrBook? selectBookMetadata(String foreignId, List<ChaptarrBook> results,
    {ChaptarrBook? initial}) {
  final selected =
      initial ?? ChaptarrBook(id: 0, title: '', foreignBookId: foreignId);
  final keys = _workKeys(selected);
  var matches = results
      .where((book) =>
          (book.foreignBookId == foreignId ||
              keys.intersection(_workKeys(book)).isNotEmpty) &&
          !_conflictingWorks(selected, book))
      .toList();
  if (matches.isEmpty) return null;
  // Conflicting explicit provider IDs name distinct records, even when one
  // shared provider ID appears on both. Do not resolve that by picking first.
  for (final a in matches) {
    if (matches.any((b) => _conflictingWorks(a, b))) return null;
  }
  if (initial != null) {
    final editionMatches =
        matches.where((book) => _sameEdition(initial, book)).toList();
    if (editionMatches.isNotEmpty) matches = editionMatches;
    final format = initial.mediaType;
    if (format != null && format.isNotEmpty) {
      final formatMatches =
          matches.where((book) => book.mediaType == format).toList();
      if (formatMatches.isNotEmpty) matches = formatMatches;
    }
  }
  if (matches.length == 1) return matches.single;

  // A title page represents both formats, but publication details must not
  // come from an arbitrary format. Use only descriptive values they agree on.
  final first = matches.first;
  final years = matches.map((book) => book.releaseDate?.year).toSet();
  final overviews = matches.map((book) => book.displayOverview ?? '').toSet();
  if (overviews.length != 1 ||
      matches.any((book) => book.title != first.title)) {
    return null;
  }
  return ChaptarrBook(
    id: 0,
    title: first.title,
    foreignBookId: foreignId,
    // A shared year is a work-level fact even when print and audio editions
    // have different publication days. Conflicting years remain unspecified.
    releaseDate: years.length == 1 && years.single != null
        ? DateTime(years.single!)
        : null,
    overview: overviews.single,
    author: matches.every(
            (book) => book.author?.authorName == first.author?.authorName)
        ? first.author
        : null,
    genres: first.genres
        .where((genre) => matches.every((book) => book.genres.contains(genre)))
        .toList(),
  );
}

/// Enrich the same selected record without changing request identity or
/// borrowing another edition's page count, date, cover, or outbound page.
ChaptarrBook enrichBookMetadata(ChaptarrBook? initial, ChaptarrBook fresh) {
  if (initial == null) return fresh;
  final sameEdition = _sameEdition(initial, fresh);
  final overview = fresh.displayOverview;
  final canAddWorkLinks = _editionKeys(initial).isEmpty &&
      _editionKeys(fresh).isEmpty &&
      initial.links.isEmpty;
  // toJson is an arr write payload and intentionally omits author/statistics;
  // construct the presentation copy directly to preserve those initial fields.
  return ChaptarrBook(
    id: initial.id,
    title: initial.title,
    authorId: initial.authorId,
    foreignBookId: initial.foreignBookId,
    foreignEditionId: sameEdition
        ? fresh.foreignEditionId ?? initial.foreignEditionId
        : initial.foreignEditionId,
    titleSlug: initial.titleSlug,
    overview:
        overview != null && overview.isNotEmpty ? overview : initial.overview,
    releaseDate: sameEdition
        ? fresh.releaseDate ?? initial.releaseDate
        : initial.releaseDate,
    monitored: initial.monitored,
    mediaType: initial.mediaType,
    seriesTitle: initial.seriesTitle,
    anyEditionOk: initial.anyEditionOk,
    pageCount: sameEdition && fresh.pageCount > 0
        ? fresh.pageCount
        : initial.pageCount,
    author: initial.author?.authorName.isNotEmpty == true
        ? initial.author
        : fresh.author,
    statistics: initial.statistics,
    editions: sameEdition && fresh.editions.isNotEmpty
        ? _enrichEditions(initial, fresh)
        : initial.editions,
    images: fresh.images.isNotEmpty &&
            (sameEdition ||
                (initial.images.isEmpty && _editionKeys(initial).isEmpty))
        ? fresh.images
        : initial.images,
    genres: fresh.genres.isNotEmpty ? fresh.genres : initial.genres,
    goodreadsBookId: sameEdition
        ? fresh.goodreadsBookId ?? initial.goodreadsBookId
        : initial.goodreadsBookId,
    goodreadsWorkId: fresh.goodreadsWorkId ?? initial.goodreadsWorkId,
    openLibraryWorkId: fresh.openLibraryWorkId ?? initial.openLibraryWorkId,
    hardcoverBookId: fresh.hardcoverBookId ?? initial.hardcoverBookId,
    links: fresh.links.isNotEmpty && (sameEdition || canAddWorkLinks)
        ? fresh.links
        : initial.links,
  );
}
