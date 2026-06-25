/// Pure matching logic for the "owned-aware book search" feature.
///
/// A book search returns [ChaptarrBook] lookup results, which carry no
/// ownership information and whose `foreignBookId` does NOT line up with the
/// user's owned records. So to decide whether the user already owns a result
/// (per format), we fuzzy-match its normalized title + author against the
/// ownership digest ([OwnedTitle] rows).
///
/// This file is intentionally free of Flutter imports so it can be unit-tested
/// in isolation.
library;

import '../../chaptarr/data/chaptarr_models.dart' show ChaptarrBook;
import '../../request/data/book_ownership.dart';

/// Minimum title similarity (token Jaccard) for a digest row to count as a
/// match for a lookup result.
const double kOwnershipTitleThreshold = 0.75;

/// Words dropped from title/author token sets before comparison. Kept small and
/// generic: articles, conjunctions, and the common "X to Y" connector so series
/// titles like "Heir to the Empire" reduce to their distinctive words.
const Set<String> _titleStopwords = {'the', 'a', 'an', 'and', 'of', 'to'};

/// Words dropped from author token sets. Authors rarely contain articles, so we
/// only strip the connectors that show up in "First and Last"-style joins.
const Set<String> _authorStopwords = {'the', 'a', 'an', 'and', 'of', '&'};

/// Matches any run of characters that are neither a Unicode letter nor a digit.
/// Used to split a normalized string into word tokens.
final RegExp _nonAlnum = RegExp(r'[^\p{L}\p{N}]+', unicode: true);

/// Matches a single trailing parenthetical/bracket group, e.g. " (Part 1)",
/// "(Book 2)", "(#3)", "[Illustrated]". Anchored to the end of the string.
final RegExp _trailingBracket = RegExp(r'\s*[\(\[][^\)\]]*[\)\]]\s*$');

/// Matches a trailing ", Book 3" / ", Vol 2" / " #3" style series suffix.
final RegExp _trailingSeriesSuffix =
    RegExp(r'[\s,]+(?:book|vol|volume|part|#)\s*\d+\s*$', caseSensitive: false);

/// Tokenizes a title for fuzzy comparison.
///
/// Steps: lowercase; (when [stripSeries]) drop a leading "Series: " prefix up to
/// and including the first ": " as long as a non-empty remainder survives; strip
/// a single trailing parenthetical/bracket and a trailing ", Book N"/"#N"
/// suffix; replace every non-alphanumeric run with a space; split on whitespace;
/// drop stopwords and empties.
List<String> normalizeTitleTokens(String s, {bool stripSeries = true}) {
  var text = s.toLowerCase();

  if (stripSeries) {
    final idx = text.indexOf(': ');
    if (idx >= 0) {
      final remainder = text.substring(idx + 2).trim();
      if (remainder.isNotEmpty) text = remainder;
    }
  }

  // Strip trailing series suffix and a trailing bracket group. Run repeatedly so
  // "Title (Book 2) [Illustrated]" collapses fully.
  var changed = true;
  while (changed) {
    changed = false;
    final afterBracket = text.replaceFirst(_trailingBracket, '');
    if (afterBracket != text) {
      text = afterBracket;
      changed = true;
    }
    final afterSuffix = text.replaceFirst(_trailingSeriesSuffix, '');
    if (afterSuffix != text) {
      text = afterSuffix;
      changed = true;
    }
  }

  return text
      .split(_nonAlnum)
      .where((t) => t.isNotEmpty && !_titleStopwords.contains(t))
      .toList();
}

/// Jaccard similarity of two token sets: |a∩b| / |a∪b|. Defined as 0 when both
/// sets are empty.
double jaccard(Set<String> a, Set<String> b) {
  if (a.isEmpty && b.isEmpty) return 0;
  final intersection = a.where(b.contains).length;
  final union = a.length + b.length - intersection;
  if (union == 0) return 0;
  return intersection / union;
}

/// Normalizes an author string into comparison tokens: lowercase, split on
/// non-alphanumerics, drop author stopwords and empties.
List<String> _authorTokens(String s) => s
    .toLowerCase()
    .split(_nonAlnum)
    .where((t) => t.isNotEmpty && !_authorStopwords.contains(t))
    .toList();

/// Whether a lookup result's author plausibly matches a digest row's author.
///
/// True when the token-set Jaccard is >= 0.6 OR the surname (last token) is the
/// same on both sides. The caller is responsible for the empty/absent-author
/// case (this returns false when either side has no usable tokens).
bool authorMatches(String? lookupAuthor, String digestAuthor) {
  if (lookupAuthor == null) return false;
  final a = _authorTokens(lookupAuthor);
  final b = _authorTokens(digestAuthor);
  if (a.isEmpty || b.isEmpty) return false;
  if (jaccard(a.toSet(), b.toSet()) >= 0.6) return true;
  return a.last == b.last;
}

/// The best title-similarity score between a result and a digest row, taking the
/// max over the series-stripped and series-kept tokenizations (so a result that
/// keeps its "Series: " prefix can still match a digest row that dropped it, and
/// vice versa).
double _titleScore(ChaptarrBook result, OwnedTitle owned) {
  final stripped = jaccard(
    normalizeTitleTokens(result.title, stripSeries: true).toSet(),
    normalizeTitleTokens(owned.title, stripSeries: true).toSet(),
  );
  final kept = jaccard(
    normalizeTitleTokens(result.title, stripSeries: false).toSet(),
    normalizeTitleTokens(owned.title, stripSeries: false).toSet(),
  );
  return stripped > kept ? stripped : kept;
}

/// Digest rows that match [result]: title score >= [kOwnershipTitleThreshold]
/// AND the authors match (or, when an author is missing on either side, the
/// title score stands alone at >= 0.9). A title can have more than one matching
/// row when its ebook and audiobook records didn't share a foreignBookId.
List<OwnedTitle> _matchingTitles(ChaptarrBook result, List<OwnedTitle> digest) {
  final lookupAuthor = result.author?.authorName;
  final lookupAuthorEmpty = lookupAuthor == null || lookupAuthor.isEmpty;
  final out = <OwnedTitle>[];
  for (final owned in digest) {
    final score = _titleScore(result, owned);
    if (score < kOwnershipTitleThreshold) continue;
    final authorOk = authorMatches(lookupAuthor, owned.author) ||
        ((lookupAuthorEmpty || owned.author.isEmpty) && score >= 0.9);
    if (authorOk) out.add(owned);
  }
  return out;
}

/// Combines several rows' ownership: a format is owned/downloaded if it is in
/// any of them (so split ebook/audiobook records merge into one truth).
BookOwnership _mergeOwnership(Iterable<BookOwnership> owns) {
  var em = false, ed = false, am = false, ad = false;
  for (final o in owns) {
    em = em || o.ebook.monitored;
    ed = ed || o.ebook.downloaded;
    am = am || o.audiobook.monitored;
    ad = ad || o.audiobook.downloaded;
  }
  return BookOwnership(
    ebook: FormatOwnership(monitored: em, downloaded: ed),
    audiobook: FormatOwnership(monitored: am, downloaded: ad),
  );
}

/// Decides whether the user already owns [result], by matching it against the
/// ownership [digest]. Returns the merged ownership across every matching digest
/// row (so a title split into separate ebook/audiobook rows still reports both),
/// or null when no row qualifies.
BookOwnership? ownershipFor(ChaptarrBook result, List<OwnedTitle> digest) {
  final matches = _matchingTitles(result, digest);
  if (matches.isEmpty) return null;
  return _mergeOwnership(matches.map((t) => t.ownership));
}

/// Owned digest titles matching the search [query] (every query token appears in
/// the title) that the [lookupResults] didn't already return — deduped by
/// normalized title+author with ownership merged. These are injected at the top
/// of the results so a book the user owns/monitors surfaces even when Chaptarr's
/// metadata search doesn't rank it.
List<OwnedTitle> ownedTitlesForQuery(
  String query,
  List<OwnedTitle> digest,
  List<ChaptarrBook> lookupResults,
) {
  final queryTokens = normalizeTitleTokens(query).toSet();
  if (queryTokens.isEmpty) return const [];

  final byKey = <String, List<OwnedTitle>>{};
  final keyOrder = <String>[];
  for (final owned in digest) {
    final titleTokens = normalizeTitleTokens(owned.title).toSet();
    if (titleTokens.isEmpty) continue;
    // The query must be contained in the title (a partial-name match).
    if (!queryTokens.every(titleTokens.contains)) continue;
    // Skip when a lookup result already represents this owned title.
    if (lookupResults.any((r) => _matchingTitles(r, [owned]).isNotEmpty)) {
      continue;
    }
    final key =
        '${titleTokens.join(' ')}|${_authorTokens(owned.author).join(' ')}';
    if (byKey[key] == null) {
      byKey[key] = [];
      keyOrder.add(key);
    }
    byKey[key]!.add(owned);
  }

  return [
    for (final key in keyOrder)
      OwnedTitle(
        title: byKey[key]!.first.title,
        author: byKey[key]!.first.author,
        year: byKey[key]!.first.year,
        ownership: _mergeOwnership(byKey[key]!.map((o) => o.ownership)),
      ),
  ];
}
