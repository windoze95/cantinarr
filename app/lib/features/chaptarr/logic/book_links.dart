import '../../media_detail/logic/title_links.dart';
import '../data/chaptarr_models.dart';

/// Chaptarr normalizes every provider id to `<letters>:<id>` (`gr:5907`,
/// `ol:OL262758W`, `hc:12345`); the prefix names the provider, not the page.
final _providerPrefix = RegExp(r'^[A-Za-z]+:');

/// A Goodreads id is digits and nothing else.
final _goodreadsId = RegExp(r'^\d+$');

/// Open Library keys: `OL<digits>W` names a work, `OL<digits>M` an edition.
final _openLibraryWork = RegExp(r'^OL\d+W$', caseSensitive: false);
final _openLibraryEdition = RegExp(r'^OL\d+M$', caseSensitive: false);

/// ISBN-13 is 13 digits; ISBN-10 is nine digits and a digit or X check.
final _isbn13 = RegExp(r'^\d{13}$');
final _isbn10 = RegExp(r'^\d{9}[\dXx]$');

/// The sites a book can be looked up on, in a fixed order: Goodreads, Open
/// Library, Hardcover. A chip exists only when the id it needs is known and
/// well formed, so nothing is ever guessed from a title. Shared by the
/// requester book page's Links line and the Chaptarr book sheet, so the two
/// never drift apart.
///
/// The ids come verbatim from Chaptarr's `BookResource`, in its prefixed form
/// (`gr:`, `ol:`, `hc:`); the prefix is stripped and the remainder has to
/// look like that provider's id, otherwise it is treated as unknown.
///
/// Goodreads opens the edition Chaptarr itself leads with: `goodreadsBookId`
/// is the Goodreads id of its leading edition (monitored first, then manually
/// added, then by id), so a reader lands on the copy the library tracks. When
/// the book carries none, the leading edition's own `goodreadsEditionId` is
/// used, then the work id as a last resort. Verified forms:
/// `https://www.goodreads.com/book/show/<id>` answers 200 and
/// `https://www.goodreads.com/work/editions/<workId>` resolves to its slug;
/// `/work/<id>` bounces to a sign-up page, so it is never used.
///
/// Open Library prefers the work (`https://openlibrary.org/works/<OL...W>`),
/// then the leading edition (`https://openlibrary.org/books/<OL...M>`), then
/// the first edition, leading first, whose ISBN-13 or ISBN-10 is well formed
/// (`https://openlibrary.org/isbn/<isbn>` resolves both).
///
/// Hardcover pages are slug-addressed, so a Hardcover chip is only honest when
/// Chaptarr's metadata declares the page itself in `links`: the first https
/// link on `hardcover.app` under `/books/`. `hardcoverBookId` alone never
/// makes one, because no page can be built from it.
List<TitleLink> bookLinks(ChaptarrBook book) {
  final editions = _orderedEditions(book.editions);
  final leading = editions.isEmpty ? null : editions.first;

  final goodreadsBook = _goodreads(book.goodreadsBookId) ??
      _goodreads(leading?.goodreadsEditionId);
  final goodreadsWork = _goodreads(book.goodreadsWorkId);
  final goodreads = goodreadsBook != null
      ? 'https://www.goodreads.com/book/show/$goodreadsBook'
      : goodreadsWork != null
          ? 'https://www.goodreads.com/work/editions/$goodreadsWork'
          : null;

  final openLibraryWork =
      _openLibrary(book.openLibraryWorkId, _openLibraryWork);
  final openLibraryEdition =
      _openLibrary(leading?.openLibraryEditionId, _openLibraryEdition);
  String? openLibrary;
  if (openLibraryWork != null) {
    openLibrary = 'https://openlibrary.org/works/$openLibraryWork';
  } else if (openLibraryEdition != null) {
    openLibrary = 'https://openlibrary.org/books/$openLibraryEdition';
  } else {
    for (final edition in editions) {
      final isbn = _isbn(edition.isbn13) ?? _isbn(edition.isbn10);
      if (isbn != null) {
        openLibrary = 'https://openlibrary.org/isbn/$isbn';
        break;
      }
    }
  }

  final hardcover = _declaredHardcoverPage(book.links);

  return [
    if (goodreads != null) TitleLink('Goodreads', goodreads),
    if (openLibrary != null) TitleLink('Open Library', openLibrary),
    if (hardcover != null) TitleLink('Hardcover', hardcover),
  ];
}

/// Chaptarr's own edition order, as far as a `BookResource` lets a client
/// reproduce it: the first monitored edition leads, else the first edition.
List<ChaptarrEdition> _orderedEditions(List<ChaptarrEdition> editions) {
  if (editions.length < 2) return editions;
  final leadIndex = editions.indexWhere((edition) => edition.monitored);
  if (leadIndex <= 0) return editions;
  return [
    editions[leadIndex],
    for (var i = 0; i < editions.length; i++)
      if (i != leadIndex) editions[i],
  ];
}

/// The id without Chaptarr's provider prefix, or empty when there is none.
String _bare(String? id) {
  final trimmed = id?.trim() ?? '';
  return trimmed.replaceFirst(_providerPrefix, '');
}

String? _goodreads(String? id) {
  final bare = _bare(id);
  return _goodreadsId.hasMatch(bare) ? bare : null;
}

String? _openLibrary(String? id, RegExp shape) {
  final bare = _bare(id);
  return shape.hasMatch(bare) ? bare.toUpperCase() : null;
}

/// A well-formed ISBN with its hyphens dropped; the same number either way,
/// and Open Library's `/isbn/` path takes it bare.
String? _isbn(String? isbn) {
  final bare = (isbn ?? '').replaceAll(RegExp(r'[-\s]'), '');
  if (_isbn13.hasMatch(bare)) return bare;
  if (_isbn10.hasMatch(bare)) return bare.toUpperCase();
  return null;
}

/// The first declared link that is an https page on `hardcover.app` (or
/// `www.hardcover.app`) under `/books/<slug>`; any other host, scheme or path
/// is not a Hardcover book page and yields nothing.
String? _declaredHardcoverPage(List<ChaptarrLink> links) {
  for (final link in links) {
    final url = link.url.trim();
    final uri = Uri.tryParse(url);
    if (uri == null || uri.scheme != 'https') continue;
    final host = uri.host.toLowerCase();
    if (host != 'hardcover.app' && host != 'www.hardcover.app') continue;
    final segments = uri.pathSegments;
    if (segments.length < 2 || segments[0] != 'books' || segments[1].isEmpty) {
      continue;
    }
    return url;
  }
  return null;
}
