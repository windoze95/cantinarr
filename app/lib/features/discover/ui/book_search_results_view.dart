import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../../chaptarr/data/chaptarr_models.dart';
import '../../chaptarr/logic/book_identity.dart';
import '../../chaptarr/logic/book_publication.dart';
import '../../dashboard/data/book_authors_service.dart';
import '../../dashboard/data/book_library_service.dart';
import '../../dashboard/logic/book_ownership_matcher.dart';
import '../../request/data/book_ownership.dart';
import '../../shell/logic/library_author_index.dart';
import '../../shell/logic/shell_book_search_provider.dart';
import 'book_metadata_prefetch.dart';

/// Native catalog rows retain their identity and order. A verified identifier
/// match adds library availability without replacing or hiding a result.
class BookSearchResultsView extends ConsumerWidget {
  final List<ChaptarrBook> results;

  /// Author matches for [query], loaded independently below the book rows:
  /// someone who typed an author's name is usually after the author, and the
  /// books by them are one tap further in.
  final List<ChaptarrAuthor> authors;
  final String query;
  final bool isLoading;

  /// True once a lookup has completed successfully for [query] — the signal
  /// that distinguishes "no books found" from "hasn't searched yet".
  final bool searched;
  final BookSearchError? error;

  /// The author lookup failed while the book lookup succeeded. An empty author
  /// section then means "could not look" rather than "nobody matched", and has
  /// to say so.
  final bool authorsUnavailable;
  final VoidCallback? onResultTap;
  final bool authorsLoading;

  /// Re-runs the search for an author the library does not hold, so their books
  /// land in this same overlay and can be requested.
  ///
  /// A metadata-only author has no page to open — Cantinarr's author screen
  /// renders library titles with ownership pills — so "show me their books" is
  /// the destination, and it is the search the user could have typed.
  final ValueChanged<String>? onAuthorDrillDown;

  const BookSearchResultsView({
    super.key,
    this.authorsLoading = false,
    required this.results,
    this.authors = const [],
    required this.query,
    required this.isLoading,
    required this.searched,
    required this.error,
    this.authorsUnavailable = false,
    this.onResultTap,
    this.onAuthorDrillDown,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (error != null) {
      // Copied character-for-character out of dashboard_books_tab.dart —
      // user-facing contract text (FAIL-01/02/03). Reword nothing,
      // interpolate nothing (threat T-03-04).
      final message = switch (error!) {
        BookSearchError.noInstance => 'No Chaptarr instance is available.',
        BookSearchError.forbidden =>
          'You do not have access to search this book library.',
        BookSearchError.requestFailed =>
          'Books could not be searched. Check the connection and try again.',
      };
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            message,
            textAlign: TextAlign.center,
            style: const TextStyle(color: AppTheme.error),
          ),
        ),
      );
    }

    if (isLoading) {
      return const Center(
        child: CircularProgressIndicator(color: AppTheme.accent),
      );
    }

    // What the user already owns, used to mark results, and to surface
    // owned/monitored books the metadata search missed.
    final digest =
        ref.watch(ownedBooksProvider).valueOrNull ?? const <OwnedTitle>[];
    final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
    final authorSummaries = instanceId == null
        ? null
        : ref.watch(bookLibraryDigestProvider(instanceId)).valueOrNull?.authors;
    final matches = [
      for (final book in results) matchBookToLibrary(book, digest)
    ];
    final injected = digest
        .where((owned) =>
            (titleMatchesQuery(query, owned.title) ||
                titleMatchesQuery(query, owned.author)) &&
            !results.any((book) => book.foreignBookId == owned.foreignBookId))
        .toList();
    // Preserve native relevance and identity. Ownership only changes badges;
    // late library results append below the returned native rows.
    final ordered = <_ResolvedBookResult>[
      for (var i = 0; i < results.length; i++)
        _ResolvedBookResult(
          book: results[i],
          ownership: matches[i].title?.ownership,
          identityAmbiguous: matches[i].ambiguous,
          sourceIdentity: 'lookup:$i',
          cover: results[i].remoteCoverUrl,
          selectedForeignId: results[i].foreignBookId?.trim() ?? '',
        ),
      for (var i = 0; i < injected.length; i++)
        _ResolvedBookResult(
          book: _ownedTitleAsBook(injected[i]),
          ownership: injected[i].ownership,
          sourceIdentity: 'library:$i',
          cover: injected[i].cover,
          selectedForeignId: injected[i].foreignBookId,
        ),
    ];

    // Resolve each looked-up author against the library's own author records.
    //
    // NOT by id: a lookup author's `id` is a metadata object's, always 0, and
    // its `foreignAuthorId` is a derived provider-priority string that need not
    // equal the library record's — see [LibraryAuthorIndex] for why the name is
    // the only shared key. An earlier version of this view read `id > 0` as
    // "in the library"; that never fired, so every author rendered as
    // metadata-only and opening one 404'd.
    final index = ref.watch(libraryAuthorIndexProvider).valueOrNull ??
        LibraryAuthorIndex.empty;
    // Authors the library holds come first, then metadata-only matches, each
    // bucket keeping Chaptarr's relevance order — the same owned-floats-up rule
    // the book rows below use, for the same reason: the record you can already
    // act on is the one you probably meant. No deduping: two records that came
    // back separately stay separate rows.
    final inLibrary = <_ResolvedAuthor>[];
    final elsewhere = <_ResolvedAuthor>[];
    for (final author in authors) {
      final match = index.match(author);
      final resolved = _ResolvedAuthor(
          lookup: author,
          match: match,
          summary: authorSummaries
              ?.where((a) => a.foreignAuthorId == match.record?.foreignAuthorId)
              .firstOrNull);
      (match.kind == LibraryAuthorMatchKind.absent ? elsewhere : inLibrary)
          .add(resolved);
    }
    // The library answers for its own authors even when the metadata lookup
    // does not: Chaptarr's search routinely returns nothing for a complete
    // author name (verified live — "madeline miller" answers empty while
    // "madeline mill" finds her), and without this the search overlay claims
    // nobody by that name exists while the Authors row behind it shows them.
    // Same query rule as the owned-title injection; records already on screen
    // through a resolved lookup row are skipped — that is the same record, not
    // a distinct one.
    final presentRecordIds = <int>{
      for (final resolved in inLibrary)
        if (resolved.record != null) resolved.record!.id,
    };
    final injectedAuthors = <_ResolvedAuthor>[
      for (final record
          in index.recordsWhere((name) => titleMatchesQuery(query, name)))
        if (!presentRecordIds.contains(record.id))
          _ResolvedAuthor(
            lookup: record,
            match: LibraryAuthorMatch(
              LibraryAuthorMatchKind.resolved,
              record,
            ),
            summary: authorSummaries
                ?.where((a) => a.foreignAuthorId == record.foreignAuthorId)
                .firstOrNull,
          ),
    ];
    final orderedAuthors = <_ResolvedAuthor>[
      ...injectedAuthors,
      ...inLibrary,
      ...elsewhere,
    ];

    if (ordered.isEmpty &&
        orderedAuthors.isEmpty &&
        !authorsLoading &&
        !authorsUnavailable) {
      // A search that ran and matched nothing says so; a search that hasn't
      // run yet (empty query, still in the idle state some caller passed
      // through) renders nothing here — the overlay isn't shown for an idle
      // query in the first place, but stay defensive about the two states.
      if (!searched) return const SizedBox.shrink();
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.menu_book,
                  size: 48, color: AppTheme.textSecondary),
              const SizedBox(height: 12),
              // Says what was actually searched. With the author lookup down
              // this cannot claim no author matched — only that no book did.
              Text(
                authorsUnavailable
                    ? 'No books found, and authors could not be searched. '
                        'Try a different search.'
                    : 'No books or authors found. Try a different search.',
                textAlign: TextAlign.center,
                style: const TextStyle(color: AppTheme.textSecondary),
              ),
            ],
          ),
        ),
      );
    }

    // Full-width scroll surface; the result column is capped and centered so
    // rows stay readable on desktop widths.
    return LayoutBuilder(builder: (context, constraints) {
      final hPad = AppBreakpoints.centeredContentPadding(
        constraints.maxWidth,
        minPadding: 0,
      );
      // One flat list of rows so authors and books share a single scroll
      // surface instead of a nested-scroll sandwich. Headers appear only when
      // both kinds are present — a books-only result set reads as it always
      // did, with no new chrome.
      //
      // Built eagerly, in two parallel lists, rather than lazily from a tagged
      // union: there is no pagination here (one Chaptarr response is the whole
      // list), and `isResult` is all the separator rule needs to know. An
      // earlier version modelled the rows as a sealed class and rendered them
      // with a `switch` expression over object patterns; that shape segfaulted
      // dart2js on Dart 3.12.0 (the SSA inliner, during `-O4 --minify`) while
      // compiling fine on 3.13.2. CI tracks `channel: stable` unpinned and the
      // Dockerfile is built by self-hosters on whatever base they have cached,
      // so this stays written in plain constructs that compile on both.
      final children = <Widget>[];
      // Parallel to [children]: true for a row that is an actual search
      // result, which is what decides whether a divider separates a pair.
      final isResult = <bool>[];
      void addRow(Widget child, {bool result = false}) {
        children.add(child);
        isResult.add(result);
      }

      if (ordered.isNotEmpty) addRow(const _SectionLabel('Books'));
      for (final result in ordered) {
        addRow(
          _BookResultTile(
            book: result.book,
            selectedForeignId: result.selectedForeignId,
            ownership: result.ownership,
            identityAmbiguous: result.identityAmbiguous,
            sourceIdentity: result.sourceIdentity,
            cover: instanceId == null
                ? null
                : chaptarrImageSource(ref, result.cover, instanceId),
            instanceId: instanceId,
            searchedTerm: query,
            onTap: onResultTap,
          ),
          result: true,
        );
      }

      if (orderedAuthors.isNotEmpty || authorsLoading || authorsUnavailable) {
        addRow(const _SectionLabel('Authors'));
      }
      for (final resolved in orderedAuthors) {
        addRow(
          _AuthorResultTile(
            resolved: resolved,
            image: instanceId == null
                ? null
                : chaptarrImageSource(ref, resolved.portraitUrl, instanceId),
            instanceId: instanceId,
            onTap: onResultTap,
            onDrillDown: onAuthorDrillDown,
          ),
          result: true,
        );
      }
      if (authorsUnavailable) {
        addRow(const _OverlayNotice('Authors could not be searched.'));
      }
      if (authorsLoading) addRow(const _OverlayNotice('Searching authors…'));

      return BookMetadataPrefetch(
        key: ValueKey('book-metadata-prefetch:$instanceId:$query'),
        child: ListView.separated(
          padding: EdgeInsets.fromLTRB(hPad, 8, hPad, 8),
          itemCount: children.length,
          // A divider belongs between two result rows, not around a header or a
          // notice — a rule under its own section label reads as a row.
          separatorBuilder: (_, i) => isResult[i] && isResult[i + 1]
              ? const Divider(height: 1, color: AppTheme.border)
              : const SizedBox(height: 8),
          itemBuilder: (_, i) {
            final child = children[i];
            if (child is _BookResultTile &&
                instanceId != null &&
                child.selectedForeignId.isNotEmpty) {
              return BookMetadataCandidate(
                metadataKey: (
                  instanceId: instanceId,
                  foreignId: child.selectedForeignId
                ),
                child: child,
              );
            }
            return child;
          },
        ),
      );
    });
  }
}

/// A group label ("Authors" / "Books") above a run of result rows.
class _SectionLabel extends StatelessWidget {
  final String title;

  const _SectionLabel(this.title);

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
        child: Text(
          title.toUpperCase(),
          style: const TextStyle(
            color: AppTheme.textSecondary,
            fontSize: 11,
            fontWeight: FontWeight.w700,
            letterSpacing: 0.8,
          ),
        ),
      );
}

/// A non-result line in the overlay — currently only "authors could not be
/// searched", which keeps a missing author section from reading as an empty one.
class _OverlayNotice extends StatelessWidget {
  final String message;

  const _OverlayNotice(this.message);

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
        child: Row(
          children: [
            const Icon(Icons.error_outline,
                size: 15, color: AppTheme.requested),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                message,
                style: const TextStyle(
                  color: AppTheme.requested,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ],
        ),
      );
}

/// One looked-up author, paired with the library record it resolved to.
class _ResolvedAuthor {
  final ChaptarrAuthor lookup;
  final LibraryAuthorMatch match;
  final LibraryAuthor? summary;

  const _ResolvedAuthor(
      {required this.lookup, required this.match, this.summary});

  /// The resolved library record supplies identity and navigation. Counts come
  /// from the same complete digest as the author shelves. Null for a
  /// metadata-only or ambiguous match.
  ChaptarrAuthor? get record => match.record;

  bool get inLibrary => match.kind == LibraryAuthorMatchKind.resolved;
  bool get ambiguous => match.kind == LibraryAuthorMatchKind.ambiguous;

  String get name => lookup.authorName;

  /// Prefer the library record's art when it resolved: that copy is the one the
  /// instance proxy can serve. Otherwise the metadata CDN portrait.
  String? get portraitUrl => record?.portraitUrl ?? lookup.portraitUrl;

  /// A missing summary means no count was read; it cannot claim an empty shelf.
  String get countLabel => summary?.countLabel ?? '';
}

/// One author search result: portrait, name, and what the library holds by
/// them.
///
/// Three shapes, by what the author actually is:
/// - **In the library** — opens the author detail screen, addressed by the
///   *library* record's `foreignAuthorId` so the server's exact-string search
///   finds it, the same way `LibraryAuthorsRow` addresses it.
/// - **Not in the library** — has no page to open (the detail screen renders
///   library titles), so tapping searches for their books instead, which is
///   the thing a requester actually wants from them.
/// - **Ambiguous** — two library records share this name. Nothing is opened;
///   the row says which choice is missing rather than guessing, mirroring how
///   `_BookResultTile` handles a contested library identity.
class _AuthorResultTile extends StatelessWidget {
  final _ResolvedAuthor resolved;
  final ChaptarrImageSource? image;
  final String? instanceId;

  /// Called right before navigating away, so the shell can dismiss the
  /// keyboard — same contract as [_BookResultTile].
  final VoidCallback? onTap;

  /// Re-runs the search for a metadata-only author's books.
  final ValueChanged<String>? onDrillDown;

  const _AuthorResultTile({
    required this.resolved,
    this.image,
    required this.instanceId,
    this.onTap,
    this.onDrillDown,
  });

  @override
  Widget build(BuildContext context) {
    final record = resolved.record;
    final libraryForeignId = record?.foreignAuthorId?.trim() ?? '';
    // Openable only with a library record that carries an id to open it by —
    // the same rule the browse row applies. An id-less record stays visible;
    // dropping the row would hide a real match.
    final canOpen =
        resolved.inLibrary && libraryForeignId.isNotEmpty && instanceId != null;
    final canDrillDown =
        !resolved.inLibrary && !resolved.ambiguous && onDrillDown != null;

    final count = resolved.countLabel;
    final String subtitle;
    if (resolved.ambiguous) {
      subtitle = 'Author';
    } else if (resolved.inLibrary) {
      subtitle = count.isEmpty ? 'Author · in your library' : 'Author · $count';
    } else {
      subtitle = 'Author · not in your library';
    }

    // Says what is missing, never guessing which record was meant.
    final String? guidance;
    if (resolved.ambiguous) {
      guidance = 'Two authors in your library share this name — open the one '
          'you want from the Authors row';
    } else if (resolved.inLibrary && !canOpen) {
      guidance = 'Ask an admin to check this author\u2019s library record';
    } else if (canDrillDown) {
      guidance = 'Tap to see their books';
    } else {
      guidance = null;
    }

    return Material(
      type: MaterialType.transparency,
      child: ListTile(
        key: ValueKey('author-result:${resolved.name}:$libraryForeignId'),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        leading: ClipOval(
          child: CachedImage(
            url: image?.url,
            headers: image?.headers,
            width: 44,
            height: 44,
            icon: Icons.person,
          ),
        ),
        title: Text(
          resolved.name,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w600),
        ),
        subtitle: Padding(
          padding: const EdgeInsets.only(top: 3),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                subtitle,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(color: AppTheme.textSecondary),
              ),
              if (guidance != null) ...[
                const SizedBox(height: 4),
                Text(
                  guidance,
                  style: TextStyle(
                    color: canDrillDown
                        ? AppTheme.textSecondary
                        : AppTheme.requested,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ],
            ],
          ),
        ),
        // Two different destinations get two different affordances, so the row
        // never promises a page it cannot open.
        trailing: canOpen
            ? const Icon(Icons.chevron_right, color: AppTheme.textSecondary)
            : canDrillDown
                ? const Icon(Icons.search, color: AppTheme.textSecondary)
                : null,
        onTap: canOpen
            ? () {
                onTap?.call();
                context.push(
                  '/detail/author/${Uri.encodeComponent(libraryForeignId)}'
                  '?name=${Uri.encodeQueryComponent(resolved.name)}'
                  '&instance_id=${Uri.encodeQueryComponent(instanceId!)}',
                );
              }
            : canDrillDown
                ? () => onDrillDown!(resolved.name)
                : null,
      ),
    );
  }
}

class _ResolvedBookResult {
  final ChaptarrBook book;
  final BookOwnership? ownership;
  final bool identityAmbiguous;
  final String sourceIdentity;
  final String? cover;
  final String selectedForeignId;

  const _ResolvedBookResult({
    required this.book,
    required this.ownership,
    this.identityAmbiguous = false,
    required this.sourceIdentity,
    required this.cover,
    required this.selectedForeignId,
  });
}

class _BookResultTile extends StatelessWidget {
  final ChaptarrBook book;
  final String selectedForeignId;
  final BookOwnership? ownership;
  final bool identityAmbiguous;
  final String sourceIdentity;
  final ChaptarrImageSource? cover;
  final String? instanceId;

  /// The term these results belong to. It travels to the detail page so a
  /// request can hand the server the search that already found this record.
  final String searchedTerm;

  /// Called right before navigating away, mirroring [SearchResultsView]'s
  /// `_SearchResultTile` — the shell dismisses the keyboard on tap.
  final VoidCallback? onTap;

  const _BookResultTile({
    required this.book,
    required this.selectedForeignId,
    this.ownership,
    this.identityAmbiguous = false,
    required this.sourceIdentity,
    this.cover,
    required this.instanceId,
    this.searchedTerm = '',
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final year = book.releaseDate?.year;
    final subtitle = <String>[
      if (book.author?.authorName.isNotEmpty ?? false) book.author!.authorName,
      if (year != null) bookPublicationYearLabel(year),
    ].join(' · ');
    // The selected native identity stays attached to metadata and submission.
    final fid = selectedForeignId.trim();
    final lookupId = book.foreignBookId?.trim() ?? '';
    final o = ownership;
    final chip = _ownershipChip(o);
    final canOpen = fid.isNotEmpty;
    final identityGuidance = identityAmbiguous
        ? 'Library match needs attention'
        : fid.isEmpty
            ? 'Ask an admin to check this book’s library record'
            : null;
    final resultKey = ValueKey('book-result:$lookupId:$fid:$sourceIdentity');
    // The shell overlay wraps this view in an opaque ColoredBox (see
    // AppShell's Positioned.fill slot), which sits between a bare ListTile
    // and its ink-splash Material ancestor and makes the splash invisible.
    // dashboard_books_tab.dart never had this problem — its ListTile lived
    // directly under the Scaffold's own Material with no opaque widget in
    // between. Give the tile its own transparent Material so ink splashes
    // paint correctly in the overlay context without changing the row's
    // appearance.
    return Material(
      type: MaterialType.transparency,
      child: ListTile(
        key: resultKey,
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        leading: ClipRRect(
          borderRadius: BorderRadius.circular(4),
          child: CachedImage(
            url: cover?.url,
            headers: cover?.headers,
            width: 44,
            height: 66,
            icon: Icons.menu_book,
          ),
        ),
        title: Text(
          book.title,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w600),
        ),
        subtitle: (subtitle.isEmpty && chip == null && identityGuidance == null)
            ? null
            : Padding(
                padding: const EdgeInsets.only(top: 3),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (subtitle.isNotEmpty)
                      Text(subtitle,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style:
                              const TextStyle(color: AppTheme.textSecondary)),
                    if (identityGuidance != null) ...[
                      if (subtitle.isNotEmpty) const SizedBox(height: 4),
                      Text(
                        identityGuidance,
                        style: const TextStyle(
                          color: AppTheme.requested,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ],
                    if (chip != null) ...[
                      if (subtitle.isNotEmpty || identityGuidance != null)
                        const SizedBox(height: 4),
                      chip,
                    ],
                  ],
                ),
              ),
        // Requests belong on the detail page. The search row has one clear
        // action: open that book, where the requester can review metadata and
        // formats.
        trailing: canOpen
            ? const Icon(Icons.chevron_right, color: AppTheme.textSecondary)
            : null,
        onTap: canOpen
            ? () {
                if (instanceId != null) {
                  BookMetadataPrefetch.openBook(
                      context, (instanceId: instanceId!, foreignId: fid));
                }
                onTap?.call();
                context.push(
                  '/detail/book/${Uri.encodeComponent(fid)}'
                  '?source=chaptarr&title=${Uri.encodeQueryComponent(book.title)}'
                  // The term that surfaced this row travels with it:
                  // requesting the book makes the server find this exact
                  // record again, and this is the search already known to
                  // return it.
                  '${searchedTerm.isEmpty ? '' : '&q=${Uri.encodeQueryComponent(searchedTerm)}'}'
                  '${instanceId == null ? '' : '&instance_id=${Uri.encodeQueryComponent(instanceId!)}'}',
                  extra: book,
                );
              }
            : null,
      ),
    );
  }
}

Widget? _ownershipChip(BookOwnership? o) {
  if (o == null || !o.anyOwned) return null;
  final states = <String>[
    if (o.ebook.downloaded)
      'eBook available'
    else if (o.ebook.monitored)
      'eBook requested',
    if (o.audiobook.downloaded)
      'Audiobook available'
    else if (o.audiobook.monitored)
      'Audiobook requested',
  ];
  // The grouped chip describes every represented format. A downloaded eBook
  // must not make the whole group look available while its audiobook is
  // still only monitored.
  final available = (!o.ebook.owned || o.ebook.downloaded) &&
      (!o.audiobook.owned || o.audiobook.downloaded);
  return _OwnershipChip(
    label: states.join(' · '),
    color: available ? AppTheme.available : AppTheme.requested,
  );
}

/// A synthetic result for an owned library title the metadata search didn't
/// return. It carries the owned record's foreignBookId, so a partly-owned
/// title (e.g. ebook present, audiobook missing) still gets a "Request more"
/// button to complete the missing format.
ChaptarrBook _ownedTitleAsBook(OwnedTitle t) => ChaptarrBook(
      id: 0,
      title: t.title,
      foreignBookId: t.foreignBookId.isNotEmpty ? t.foreignBookId : null,
      author: ChaptarrAuthorContext(id: 0, authorName: t.author),
      releaseDate: t.year > 0 ? DateTime(t.year) : null,
    );

/// A small colored pill marking that a search result is already in the
/// library.
class _OwnershipChip extends StatelessWidget {
  final String label;
  final Color color;

  const _OwnershipChip({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(label,
          style: TextStyle(
              color: color, fontSize: 10.5, fontWeight: FontWeight.w600)),
    );
  }
}
