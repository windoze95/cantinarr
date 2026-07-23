import '../../chaptarr/data/chaptarr_models.dart';
import 'book_ownership_matcher.dart';

/// A search result paired with a deterministic relevance score and the
/// plain-language evidence used to explain why it ranks where it does.
class RankedBookSearchResult {
  final ChaptarrBook book;
  final int originalIndex;
  final int score;
  final String evidence;
  final bool recommendationEligible;

  const RankedBookSearchResult({
    required this.book,
    required this.originalIndex,
    required this.score,
    required this.evidence,
    required this.recommendationEligible,
  });
}

/// Ranks every distinct record without dropping or merging any of them.
///
/// Identity beats metadata completeness: an exact title or author match always
/// stays ahead of a merely well-described result. Chaptarr's original order is
/// the final tie-breaker, which makes equal candidates stable across rebuilds.
List<RankedBookSearchResult> rankBookSearchResults(
  String query,
  List<ChaptarrBook> books,
) {
  final ranked = <RankedBookSearchResult>[];
  for (var index = 0; index < books.length; index++) {
    final scored = _scoreBook(query, books[index]);
    ranked.add(RankedBookSearchResult(
      book: books[index],
      originalIndex: index,
      score: scored.score,
      evidence: scored.evidence,
      recommendationEligible: scored.recommendationEligible,
    ));
  }
  ranked.sort((a, b) {
    final byScore = b.score.compareTo(a.score);
    return byScore != 0 ? byScore : a.originalIndex.compareTo(b.originalIndex);
  });
  return ranked;
}

/// Returns one autocomplete-style fallback by removing the final Unicode code
/// point from [query]. This is deliberately bounded to one retry: Chaptarr can
/// cache an empty exact lookup even while the immediately shorter prefix still
/// returns the intended work.
String? bookSearchPrefixFallbackTerm(String query) {
  final trimmed = query.trim();
  final codePoints = trimmed.runes.toList(growable: false);
  if (codePoints.length <= 1) return null;

  final prefix = String.fromCharCodes(
    codePoints.take(codePoints.length - 1),
  ).trimRight();
  return prefix.isEmpty ? null : prefix;
}

/// Whether a fallback row still contains the requester's complete query as a
/// normalized title or author phrase. The prefix retry may be broader than the
/// exact lookup, so unrelated autocomplete rows must not leak into the result
/// list. Every distinct row that passes remains visible.
bool stronglyMatchesBookSearch(String query, ChaptarrBook book) {
  final queryText = _normalizedText(query);
  if (queryText.isEmpty) return false;

  return _containsNormalizedPhrase(_normalizedText(book.title), queryText) ||
      _containsNormalizedPhrase(
        _normalizedText(book.author?.authorName ?? ''),
        queryText,
      );
}

bool _containsNormalizedPhrase(String candidate, String query) {
  if (candidate == query) return true;
  return candidate.startsWith('$query ') ||
      candidate.endsWith(' $query') ||
      candidate.contains(' $query ');
}

({int score, String evidence, bool recommendationEligible}) _scoreBook(
  String query,
  ChaptarrBook book,
) {
  final queryText = _normalizedText(query);
  final titleText = _normalizedText(book.title);
  final authorText = _normalizedText(book.author?.authorName ?? '');
  final queryTokens = normalizeTitleTokens(query, stripSeries: false).toSet();
  final titleTokens = normalizeTitleTokens(book.title, stripSeries: false).toSet();
  final strippedTitleTokens = normalizeTitleTokens(book.title).toSet();
  final authorTokens = normalizeTitleTokens(
    book.author?.authorName ?? '',
    stripSeries: false,
  ).toSet();

  var score = 0;
  var evidence = 'Closest title match';
  var confidentMatch = false;
  if (queryText.isNotEmpty && titleText == queryText) {
    score += 1200;
    evidence = 'Exact title match';
    confidentMatch = true;
  } else if (queryText.isNotEmpty && titleText.startsWith(queryText)) {
    score += 900;
    evidence = 'Title starts with your search';
    confidentMatch = true;
  } else if (queryText.isNotEmpty && titleText.contains(queryText)) {
    score += 720;
    evidence = 'Strong title match';
    confidentMatch = true;
  }

  if (queryTokens.isNotEmpty) {
    final titleCoverage = queryTokens.where(titleTokens.contains).length /
        queryTokens.length;
    final strippedCoverage =
        queryTokens.where(strippedTitleTokens.contains).length /
            queryTokens.length;
    final bestTitleCoverage =
        titleCoverage > strippedCoverage ? titleCoverage : strippedCoverage;
    score += (bestTitleCoverage * 420).round();
    if (bestTitleCoverage == 1) confidentMatch = true;

    final authorCoverage =
        queryTokens.where(authorTokens.contains).length / queryTokens.length;
    if (authorCoverage == 1) {
      score += authorText == queryText ? 1050 : 620;
      confidentMatch = true;
      if (titleText != queryText) {
        evidence = authorText == queryText
            ? 'Exact author match'
            : 'Strong author match';
      }
    } else {
      score += (authorCoverage * 240).round();
    }
  }

  // Completeness only resolves close matches; it can never overcome a title or
  // author identity difference on its own.
  if (book.releaseDate != null) score += 12;
  if ((book.displayOverview ?? '').isNotEmpty) score += 10;
  if (book.displayPageCount > 0) score += 6;
  if (book.remoteCoverUrl != null || book.coverUrl != null) score += 5;
  if (book.editions.isNotEmpty) score += 5;
  if (book.author?.authorName.isNotEmpty ?? false) score += 4;

  final companion = _isObviousCompanionTitle(titleText) ||
      RegExp(r'^\s*summary\s+of\b', caseSensitive: false)
          .hasMatch(book.title);
  final multiBookSet = _isObviousMultiBookSet(book.title, titleText);
  if (companion) {
    score -= 10000;
    evidence = 'Companion or study material';
  }
  if (multiBookSet) {
    score -= 10000;
    evidence = 'Multiple-book set';
  }

  return (
    score: score,
    evidence: evidence,
    recommendationEligible:
        confidentMatch && !companion && !multiBookSet,
  );
}

const _companionMarkers = <String>[
  'study guide',
  'sparknotes',
  'cliffsnotes',
  'summary analysis',
  'unofficial companion',
  'lesson plan',
  'conversation starters',
];

bool _isObviousCompanionTitle(String normalizedTitle) =>
    _companionMarkers.any(normalizedTitle.contains);

final RegExp _completeSeries = RegExp(r'\bcomplete\b.*\bseries\b');
final RegExp _numberedBookRange =
    RegExp(
      r'\bbooks?\s+\d+\s*(?:[-–—&+,]|to|through)\s*\d+\b',
      caseSensitive: false,
    );

bool _isObviousMultiBookSet(String rawTitle, String normalizedTitle) {
  if (RegExp(r'\bomnibus\b').hasMatch(normalizedTitle)) return true;
  if (normalizedTitle.contains('box set') ||
      normalizedTitle.contains('boxed set')) {
    return true;
  }
  if (normalizedTitle == 'bundle' ||
      normalizedTitle.endsWith(' bundle') ||
      normalizedTitle == 'trilogy' ||
      normalizedTitle.endsWith(' trilogy')) {
    return true;
  }
  return _completeSeries.hasMatch(normalizedTitle) ||
      _numberedBookRange.hasMatch(rawTitle);
}

String _normalizedText(String value) => normalizeTitleTokens(
      value,
      stripSeries: false,
    ).join(' ');

/// Whether two provider rows describe the same work, before considering a
/// particular publication/edition. Different records remain visible in search;
/// this relation is used only to decide which records belong in one request
/// wizard.
bool sameBookWork(ChaptarrBook a, ChaptarrBook b) {
  if (!_sameWorkTitle(a.title, b.title)) return false;

  final aAuthor = a.author?.authorName.trim() ?? '';
  final bAuthor = b.author?.authorName.trim() ?? '';
  // A common title alone is not enough to claim two provider records are the
  // same work. If either author is missing, leave each row independent so the
  // requester can act on the exact result they chose.
  if (aAuthor.isEmpty || bAuthor.isEmpty) return false;
  return strongAuthorMatch(aAuthor, bAuthor);
}

bool _sameWorkTitle(String a, String b) {
  final aTokens = normalizeTitleTokens(a, stripSeries: false).toSet();
  final bTokens = normalizeTitleTokens(b, stripSeries: false).toSet();
  if (aTokens.isEmpty || bTokens.isEmpty) return false;
  if (_setEquals(aTokens, bTokens) || strongNormalizedTitleMatch(a, b)) {
    return true;
  }

  // Chaptarr can return the same work once with its plain title and again with
  // a subtitle or edition label. Treat only explicit subtitle separators as a
  // possible shared base; a plain shared prefix is deliberately insufficient.
  final aBase = _subtitleBaseTokens(a);
  final bBase = _subtitleBaseTokens(b);
  return (aBase.isNotEmpty &&
          (_setEquals(aBase, bTokens) || _setEquals(aBase, bBase))) ||
      (bBase.isNotEmpty && _setEquals(bBase, aTokens));
}

final RegExp _subtitleSeparator = RegExp(r'\s*(?::|[–—])\s*|\s+-\s+');

Set<String> _subtitleBaseTokens(String title) {
  final match = _subtitleSeparator.firstMatch(title);
  if (match == null || match.start == 0) return const <String>{};
  return normalizeTitleTokens(
    title.substring(0, match.start),
    stripSeries: false,
  ).toSet();
}

/// True when the metadata exposes no meaningful choice to a requester.
/// Missing metadata is treated as unknown, not as a different edition. Two
/// non-empty, conflicting values do make the rows distinct.
bool booksAreIndistinguishableForRequester(
  ChaptarrBook a,
  ChaptarrBook b,
) {
  if (!sameBookWork(a, b)) return false;
  if (_conflictingInt(a.releaseDate?.year, b.releaseDate?.year)) return false;
  if (_conflictingInt(a.displayPageCount, b.displayPageCount)) return false;
  if (_conflictingSet(_editionTitles(a), _editionTitles(b))) return false;
  if (_conflictingSet(_publishers(a), _publishers(b))) return false;
  if (_conflictingSet(_isbns(a), _isbns(b))) return false;
  return true;
}

bool _conflictingInt(int? a, int? b) =>
    a != null && a > 0 && b != null && b > 0 && a != b;

bool _conflictingSet(Set<String> a, Set<String> b) =>
    a.isNotEmpty && b.isNotEmpty && !_setEquals(a, b);

bool _setEquals(Set<String> a, Set<String> b) =>
    a.length == b.length && a.every(b.contains);

Set<String> _editionTitles(ChaptarrBook book) => book.editions
    .map((edition) => _normalizedText(edition.title ?? ''))
    .where((title) => title.isNotEmpty && title != _normalizedText(book.title))
    .toSet();

Set<String> _publishers(ChaptarrBook book) => book.editions
    .map((edition) => _normalizedText(edition.publisher ?? ''))
    .where((publisher) => publisher.isNotEmpty)
    .toSet();

Set<String> _isbns(ChaptarrBook book) => book.editions
    .map((edition) => (edition.isbn13 ?? '').replaceAll(RegExp(r'[^0-9Xx]'), ''))
    .where((isbn) => isbn.isNotEmpty)
    .toSet();

/// User-recognizable facts that can distinguish one edition card from another.
/// Raw Chaptarr record IDs and internal provider fields are intentionally
/// omitted.
List<String> bookEditionFacts(ChaptarrBook book) {
  final facts = <String>[];
  final year = book.releaseDate?.year;
  if (year != null && year > 0) facts.add('$year');

  final publishers = book.editions
      .map((edition) => edition.publisher?.trim() ?? '')
      .where((publisher) => publisher.isNotEmpty)
      .toSet();
  facts.addAll(publishers.take(2));

  final editionTitles = book.editions
      .map((edition) => edition.title?.trim() ?? '')
      .where((title) =>
          title.isNotEmpty && _normalizedText(title) != _normalizedText(book.title))
      .toSet();
  facts.addAll(editionTitles.take(2));

  if (book.displayPageCount > 0) {
    facts.add('${book.displayPageCount} pages');
  }

  final isbns = book.editions
      .map((edition) => edition.isbn13?.trim() ?? '')
      .where((isbn) => isbn.isNotEmpty)
      .toSet();
  if (isbns.length == 1) facts.add('ISBN ${isbns.single}');
  return facts;
}
