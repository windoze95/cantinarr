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

bool strongNormalizedTitleMatch(String a, String b) {
  final aTokens = normalizeTitleTokens(a).toSet();
  final bTokens = normalizeTitleTokens(b).toSet();
  return aTokens.isNotEmpty && bTokens.isNotEmpty && jaccard(aTokens, bTokens) == 1;
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

/// Strong author identity for provider-ID fallback. Unlike [authorMatches],
/// this does not accept a shared surname by itself: every normalized author
/// token must match, though token order may differ.
bool strongAuthorMatch(String? lookupAuthor, String digestAuthor) {
  if (lookupAuthor == null) return false;
  final lookup = _authorTokens(lookupAuthor).toSet();
  final digest = _authorTokens(digestAuthor).toSet();
  return lookup.isNotEmpty &&
      digest.isNotEmpty &&
      jaccard(lookup, digest) == 1;
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
/// row when the user owns several near-identically-titled records.
List<OwnedTitle> ownedMatchesFor(
  ChaptarrBook result,
  List<OwnedTitle> digest,
) {
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

bool _sameForeignId(ChaptarrBook result, OwnedTitle owned) {
  final lookupId = result.foreignBookId?.trim() ?? '';
  return lookupId.isNotEmpty && lookupId == owned.foreignBookId.trim();
}

/// Identity candidates prefer an exact nonempty foreign id. Fuzzy metadata is
/// considered only when the provider's lookup id does not identify a digest
/// row (the common mismatched-id case).
List<OwnedTitle> ownedIdentityCandidatesFor(
  ChaptarrBook result,
  List<OwnedTitle> digest,
) {
  final exact = digest.where((owned) => _sameForeignId(result, owned)).toList();
  return exact.isNotEmpty ? exact : ownedMatchesFor(result, digest);
}

bool _strongIdentityMatch(ChaptarrBook result, OwnedTitle owned) {
  if (_titleScore(result, owned) < 0.999) return false;
  final lookupAuthor = _authorTokens(result.author?.authorName ?? '').toSet();
  final digestAuthor = _authorTokens(owned.author).toSet();
  if (lookupAuthor.isEmpty || digestAuthor.isEmpty) return false;
  return jaccard(lookupAuthor, digestAuthor) == 1;
}

/// The single digest row [result] matches. Ambiguity fails closed rather than
/// choosing a first record and potentially requesting the wrong library item.
OwnedTitle? ownedMatchFor(ChaptarrBook result, List<OwnedTitle> digest) {
  final matches = ownedIdentityCandidatesFor(result, digest);
  return matches.length == 1 &&
          (_sameForeignId(result, matches.single) ||
              _strongIdentityMatch(result, matches.single))
      ? matches.single
      : null;
}

/// One-to-one lookup-to-library mappings. A mapping is safe only when a lookup
/// row has exactly one digest candidate and no other lookup row makes an
/// equally strong claim on that digest row. This rejects ambiguity in either
/// direction.
///
/// Exact and fuzzy claims are counted apart, because they are not the same kind
/// of statement: an id the library itself files a record under identifies it,
/// while a same-title sibling only resembles it. A resemblance therefore cannot
/// cancel an identity — otherwise requesting a book leaves the very row that
/// created the library record unable to recognise it.
Map<ChaptarrBook, OwnedTitle> unambiguousOwnedMatches(
  List<ChaptarrBook> lookupResults,
  List<OwnedTitle> digest,
) {
  final candidates = <ChaptarrBook, List<OwnedTitle>>{};
  final exactClaims = <OwnedTitle, int>{};
  final fuzzyClaims = <OwnedTitle, int>{};
  for (final result in lookupResults) {
    final matches = ownedIdentityCandidatesFor(result, digest);
    candidates[result] = matches;
    for (final match in matches) {
      final claims = _sameForeignId(result, match) ? exactClaims : fuzzyClaims;
      claims[match] = (claims[match] ?? 0) + 1;
    }
  }

  final resolved = <ChaptarrBook, OwnedTitle>{};
  for (final entry in candidates.entries) {
    if (entry.value.length != 1) continue;
    final match = entry.value.single;
    if (_sameForeignId(entry.key, match)) {
      // Two lookup rows carrying the same library id are still ambiguous —
      // only a fuzzy contender is outranked.
      if ((exactClaims[match] ?? 0) == 1) resolved[entry.key] = match;
      continue;
    }
    if ((exactClaims[match] ?? 0) == 0 &&
        (fuzzyClaims[match] ?? 0) == 1 &&
        _strongIdentityMatch(entry.key, match)) {
      resolved[entry.key] = match;
    }
  }
  return resolved;
}

/// How a book search's lookup rows line up with the requester's library,
/// resolved once so every part of the results list agrees.
class BookSearchIdentity {
  /// Safe one-to-one lookup-to-library bindings (see [unambiguousOwnedMatches]).
  final Map<ChaptarrBook, OwnedTitle> matches;

  /// Lookup rows that plausibly are a library record but could not be tied to
  /// one, valued by the records each might be. These rows must not claim owned
  /// state or target a library id — but they still address a real metadata
  /// record, so they stay openable.
  final Map<ChaptarrBook, List<OwnedTitle>> contested;

  /// Library records to surface as their own result rows.
  final List<OwnedTitle> libraryRows;

  const BookSearchIdentity({
    required this.matches,
    required this.contested,
    required this.libraryRows,
  });
}

/// Resolves [lookupResults] against [digest] for the search [query].
BookSearchIdentity resolveBookSearchIdentity({
  required String query,
  required List<ChaptarrBook> lookupResults,
  required List<OwnedTitle> digest,
}) {
  final matches = unambiguousOwnedMatches(lookupResults, digest);
  final bound = matches.values.toSet();

  final contested = <ChaptarrBook, List<OwnedTitle>>{};
  for (final result in lookupResults) {
    if (matches.containsKey(result)) continue;
    final candidates = ownedIdentityCandidatesFor(result, digest)
        .where((owned) => !bound.contains(owned))
        .toList(growable: false);
    if (candidates.isNotEmpty) contested[result] = candidates;
  }
  final needsChoosing = <OwnedTitle>{
    for (final candidates in contested.values) ...candidates,
  };

  // Concrete library records not already represented by a safe one-to-one
  // lookup mapping: the ones the query names, plus every record an unresolved
  // row might be. That second group is what makes "choose a matching library
  // record" a real instruction — an author search never names a title, so the
  // record the row means would otherwise not be on screen at all.
  final libraryRows = <OwnedTitle>[];
  for (final owned in digest) {
    if (bound.contains(owned)) continue;
    if (needsChoosing.contains(owned)) {
      libraryRows.add(owned);
      continue;
    }
    // Only surface books the user actually has or is monitoring — not empty
    // library shells (all-missing, unmonitored duplicate records).
    if (!owned.ownership.anyOwned && owned.statusKnown) continue;
    if (!titleMatchesQuery(query, owned.title)) continue;
    libraryRows.add(owned);
  }

  return BookSearchIdentity(
    matches: matches,
    contested: contested,
    libraryRows: libraryRows,
  );
}

/// Decides whether the user already owns [result], by matching it against the
/// ownership [digest] — a single real record's ownership (see [ownedMatchFor]),
/// not a blend. Null when no row qualifies.
BookOwnership? ownershipFor(ChaptarrBook result, List<OwnedTitle> digest) =>
    ownedMatchFor(result, digest)?.ownership;

/// Whether [title] answers the search [query]: every query word appears in the
/// title, the last one as a prefix.
///
/// The trailing word is matched as a prefix because search runs on every
/// keystroke — the word being typed right now must not disqualify a requester's
/// own library from their own search.
///
/// Both sides are compared under both tokenizations. The series-stripped form
/// drops everything before the first ": ", which is right for "Series: Title"
/// but would otherwise throw away the headline of "Headline: Subtitle" — the
/// very words a requester types first.
bool titleMatchesQuery(String query, String title) {
  final queries = _tokenizations(query);
  final titles = _tokenizations(title);
  for (final queryTokens in queries) {
    if (queryTokens.isEmpty) continue;
    for (final titleTokens in titles) {
      if (_tokensContain(titleTokens, queryTokens)) return true;
    }
  }
  return false;
}

/// A string's series-kept and series-stripped token lists (identical, and so
/// collapsed to one, when the string has no "Prefix: " to strip).
List<List<String>> _tokenizations(String text) {
  final kept = normalizeTitleTokens(text, stripSeries: false);
  final stripped = normalizeTitleTokens(text);
  if (kept.length == stripped.length) return [kept];
  return [kept, stripped];
}

bool _tokensContain(List<String> titleTokens, List<String> queryTokens) {
  if (titleTokens.isEmpty) return false;
  for (var i = 0; i < queryTokens.length; i++) {
    final token = queryTokens[i];
    final partial = i == queryTokens.length - 1;
    final present = titleTokens.any(
      (candidate) => partial ? candidate.startsWith(token) : candidate == token,
    );
    if (!present) return false;
  }
  return true;
}

/// Owned digest titles to surface as their own search rows, each kept as its own
/// entry so the user sees and picks among their distinct records — nothing is
/// merged or deduplicated by title. See [resolveBookSearchIdentity], which owns
/// the rule.
List<OwnedTitle> ownedTitlesForQuery(
  String query,
  List<OwnedTitle> digest,
  List<ChaptarrBook> lookupResults,
) =>
    resolveBookSearchIdentity(
      query: query,
      lookupResults: lookupResults,
      digest: digest,
    ).libraryRows;
