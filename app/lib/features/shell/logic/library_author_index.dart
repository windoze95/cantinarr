import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../chaptarr/data/chaptarr_api_service.dart';
import '../../chaptarr/data/chaptarr_models.dart';

/// How a metadata-lookup author relates to the Chaptarr library.
enum LibraryAuthorMatchKind {
  /// Exactly one library author carries this name.
  resolved,

  /// More than one library author carries this name. Which record the user
  /// means is genuinely unknown, so nothing is opened and the row says so.
  ambiguous,

  /// No library author carries this name — a metadata-only match.
  absent,
}

/// The outcome of resolving one lookup author against the library.
class LibraryAuthorMatch {
  final LibraryAuthorMatchKind kind;

  /// The single library record, when [kind] is
  /// [LibraryAuthorMatchKind.resolved]. Null otherwise — including for
  /// `ambiguous`, where picking one would be a guess.
  final ChaptarrAuthor? record;

  const LibraryAuthorMatch(this.kind, [this.record]);

  static const absent = LibraryAuthorMatch(LibraryAuthorMatchKind.absent);
  static const ambiguous = LibraryAuthorMatch(LibraryAuthorMatchKind.ambiguous);
}

/// Normalizes an author name into a join key.
///
/// Lowercased, whitespace collapsed, and `.` / `,` dropped so "Ursula K. Le
/// Guin" and "Ursula K Le Guin" are the same key. This is **exact equality on a
/// normalized key**, deliberately not fuzzy matching: per AGENTS.md no fuzzy
/// title/author fallback may be reachable from a render path, and a token-overlap
/// score here would open the wrong author's page.
String normalizeAuthorName(String name) => name
    .toLowerCase()
    .replaceAll(RegExp(r'[.,]'), '')
    .replaceAll(RegExp(r'\s+'), ' ')
    .trim();

/// A name-keyed view of the Chaptarr library's own author records.
///
/// **Why a name key and not an id.** Chaptarr's `author/lookup` is a pure
/// metadata search (`SearchForNewAuthor` projects authors out of
/// `SearchForNewBook`), so its records are never the local library's. Worse, the
/// `foreignAuthorId` both sides expose is a *derived* field: Chaptarr's
/// `AuthorResource` picks Hardcover's id if the record has one, else Goodreads',
/// else Audnexus'. A library record added when only a Goodreads id was known
/// therefore reports `gr:…` while a fresh search for the same author reports
/// `hc:…`, and the individual provider ids are not exposed to compare instead.
/// The two id strings simply cannot be matched, which is why opening a
/// looked-up author by its own `foreignAuthorId` 404s against
/// `GetLibraryAuthorDetailForInstance`'s exact-string search. The name is the
/// only key both sides share.
///
/// Names are not unique, so a key with two records resolves to
/// [LibraryAuthorMatchKind.ambiguous] rather than picking one.
class LibraryAuthorIndex {
  final Map<String, List<ChaptarrAuthor>> _byName;

  const LibraryAuthorIndex(this._byName);

  static const empty = LibraryAuthorIndex(<String, List<ChaptarrAuthor>>{});

  factory LibraryAuthorIndex.from(List<ChaptarrAuthor> libraryAuthors) {
    final byName = <String, List<ChaptarrAuthor>>{};
    for (final author in libraryAuthors) {
      final key = normalizeAuthorName(author.authorName);
      // An unnamed record cannot be joined by name, and an empty key would
      // collide every other unnamed record onto it.
      if (key.isEmpty) continue;
      byName.putIfAbsent(key, () => <ChaptarrAuthor>[]).add(author);
    }
    return LibraryAuthorIndex(byName);
  }

  /// Resolves [lookupAuthor] against the library.
  LibraryAuthorMatch match(ChaptarrAuthor lookupAuthor) {
    final key = normalizeAuthorName(lookupAuthor.authorName);
    if (key.isEmpty) return LibraryAuthorMatch.absent;
    final hits = _byName[key];
    if (hits == null || hits.isEmpty) return LibraryAuthorMatch.absent;
    if (hits.length > 1) return LibraryAuthorMatch.ambiguous;
    return LibraryAuthorMatch(LibraryAuthorMatchKind.resolved, hits.first);
  }
}

/// The active Chaptarr instance's own author records, indexed by name, for
/// resolving search results to openable library records.
///
/// This is the library's full author list (`GET /author`), not the browse row's
/// (`/api/requests/book-authors`): that one is capped at 200 by design — "a
/// shelf to scan, not the library" — and search is exactly the path that
/// reaches past the cap. One unpaginated fetch per instance, cached for the
/// session, issued only once a Books-tab search actually needs it.
///
/// On failure this yields [LibraryAuthorIndex.empty] rather than throwing: a
/// book search must not fail because author *linking* could not be resolved.
/// Every author then reads as metadata-only, which is the safe direction — no
/// row claims to be in a library this could not read.
final libraryAuthorIndexProvider =
    FutureProvider.autoDispose<LibraryAuthorIndex>((ref) async {
  final instance = ref.watch(instanceProvider).activeChaptarrInstance;
  if (instance == null) return LibraryAuthorIndex.empty;
  ref.keepAlive();
  final service = ChaptarrApiService(
    backendDio: ref.read(backendClientProvider),
    instanceId: instance.id,
  );
  try {
    return LibraryAuthorIndex.from(await service.getAuthors());
  } catch (_) {
    return LibraryAuthorIndex.empty;
  }
});
