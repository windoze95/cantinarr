import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../chaptarr/data/chaptarr_api_service.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../../chaptarr/data/chaptarr_models.dart';
import '../../request/data/book_ownership.dart';
import '../../request/data/request_service.dart';
import '../../request/ui/book_request_button.dart';
import '../data/book_library_service.dart';
import '../logic/book_ownership_matcher.dart';

/// Dashboard Books tab: search Chaptarr's catalog (books/authors) and request a
/// book. Chaptarr lookup is search-only (no "popular" feed like TMDB), so this
/// tab is search-first; the full library lives in the Books module.
class DashboardBooksTab extends ConsumerStatefulWidget {
  const DashboardBooksTab({super.key});

  @override
  ConsumerState<DashboardBooksTab> createState() => _DashboardBooksTabState();
}

class _DashboardBooksTabState extends ConsumerState<DashboardBooksTab>
    with WidgetsBindingObserver {
  final _controller = TextEditingController();
  Timer? _debounce;
  List<ChaptarrBook> _results = [];
  bool _isSearching = false;
  bool _searched = false;
  String? _error;
  int _searchGen = 0; // guards against superseded async results
  String _searchedTerm = ''; // term the current _results belong to

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _debounce?.cancel();
    _controller.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _refreshBookTruth();
  }

  void _refreshBookTruth() {
    final instanceId =
        ref.read(instanceProvider).activeChaptarrInstance?.id;
    ref.invalidate(ownedBooksForInstanceProvider(instanceId));
    ref.invalidate(ownedBooksProvider);
    ref.read(libraryRefreshTickProvider.notifier).state++;
  }

  // Search as the user types (debounced) so results appear without having to
  // hit the keyboard's submit key; also refreshes the clear-button affordance.
  void _onChanged() {
    setState(() {});
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 400), _search);
  }

  ChaptarrApiService? _chaptarr() {
    final instance = ref.read(instanceProvider).activeChaptarrInstance;
    if (instance == null) return null;
    return ChaptarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instance.id,
    );
  }

  Future<void> _search() async {
    final term = _controller.text.trim();
    if (term.isEmpty) {
      _searchGen++;
      setState(() {
        _results = [];
        _searchedTerm = '';
        _isSearching = false;
        _searched = false;
        _error = null;
      });
      return;
    }
    final service = _chaptarr();
    if (service == null) {
      setState(() => _error = 'No Chaptarr instance is available.');
      return;
    }
    final gen = ++_searchGen;
    setState(() {
      _isSearching = true;
      _error = null;
    });
    try {
      final books = await service.lookupBook(term);
      if (!mounted || gen != _searchGen) return;
      setState(() {
        _results = books;
        _searchedTerm = term;
        _isSearching = false;
        _searched = true;
      });
    } on DioException catch (e) {
      if (!mounted || gen != _searchGen) return;
      final code = e.response?.statusCode;
      setState(() {
        _isSearching = false;
        _searched = true;
        _error = code == 401 || code == 403
            ? 'You do not have access to search this book library.'
            : 'Books could not be searched. Check the connection and try again.';
      });
    } catch (_) {
      if (!mounted || gen != _searchGen) return;
      setState(() {
        _isSearching = false;
        _searched = true;
        _error = 'Books could not be searched. Check the connection and try again.';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.hasValue) _refreshBookTruth();
    });
    ref.listen(
      instanceProvider.select((state) => state.activeChaptarrInstance?.id),
      (previous, next) {
        if (previous == next) return;
        _searchGen++;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (!mounted) return;
          setState(() {
            _results = [];
            _searchedTerm = '';
            _searched = false;
            _isSearching = false;
            _error = null;
          });
          ref.invalidate(ownedBooksProvider);
          if (_controller.text.trim().isNotEmpty) _search();
        });
      },
    );
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(12),
          child: TextField(
            controller: _controller,
            textInputAction: TextInputAction.search,
            onChanged: (_) => _onChanged(),
            onSubmitted: (_) {
              _debounce?.cancel();
              _search();
            },
            style: const TextStyle(color: AppTheme.textPrimary),
            decoration: InputDecoration(
              hintText: 'Search books or authors…',
              hintStyle: const TextStyle(color: AppTheme.textSecondary),
              prefixIcon:
                  const Icon(Icons.search, color: AppTheme.textSecondary),
              suffixIcon: _controller.text.isEmpty
                  ? null
                  : IconButton(
                      icon: const Icon(Icons.clear,
                          color: AppTheme.textSecondary),
                      onPressed: () {
                        _controller.clear();
                        _search();
                      },
                    ),
              filled: true,
              fillColor: AppTheme.surface,
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
                borderSide: BorderSide.none,
              ),
            ),
          ),
        ),
        Expanded(child: _buildBody()),
      ],
    );
  }

  Widget _buildBody() {
    if (_isSearching) {
      return const Center(
        child: CircularProgressIndicator(color: AppTheme.accent),
      );
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(_error!,
              textAlign: TextAlign.center,
              style: const TextStyle(color: AppTheme.error)),
        ),
      );
    }
    // What the user already owns, used to mark results, gate per-format
    // requests, and surface owned/monitored books the metadata search missed.
    final digest =
        ref.watch(ownedBooksProvider).valueOrNull ?? const <OwnedTitle>[];
    // Concrete library records not already represented by a safe one-to-one
    // lookup mapping. Ambiguous candidates are shown separately here so the
    // requester can choose a real record rather than targeting a fuzzy guess.
    final injected = digest.isEmpty
        ? const <OwnedTitle>[]
        : ownedTitlesForQuery(_searchedTerm, digest, _results);
    // Mark each lookup result with its ownership and float owned titles to the
    // top, preserving Chaptarr's relevance order within each bucket (don't
    // collapse versions — the user wants to see ones they don't own). Only owned
    // results carry a cover: the owned record's cached /MediaCover, which loads
    // with the API key. Lookup (/MediaCoverProxy) covers are broken server-side
    // in this fork, so we don't attempt them — not-yet-owned rows stay iconic.
    final safeMatches = unambiguousOwnedMatches(_results, digest);
    final owned = <_ResolvedBookResult>[];
    final rest = <_ResolvedBookResult>[];
    for (var lookupIndex = 0;
        lookupIndex < _results.length;
        lookupIndex++) {
      final book = _results[lookupIndex];
      final candidates =
          digest.isEmpty
              ? const <OwnedTitle>[]
              : ownedIdentityCandidatesFor(book, digest);
      final match = safeMatches[book];
      final identityAmbiguous = candidates.isNotEmpty && match == null;
      final cover =
          (match != null && match.cover.isNotEmpty) ? match.cover : null;
      final libraryId = match?.foreignBookId.trim() ?? '';
      final lookupId = book.foreignBookId?.trim() ?? '';
      ((match?.ownership.anyOwned ?? false) ? owned : rest)
          .add(_ResolvedBookResult(
            book: book,
            ownership: match?.ownership,
            ownershipStatusKnown: match?.statusKnown ?? true,
            identityAmbiguous: identityAmbiguous,
            sourceIdentity: 'lookup:$lookupIndex',
            cover: cover,
            canonicalForeignId:
                libraryId.isNotEmpty ? libraryId : lookupId,
          ));
    }
    final ordered = <_ResolvedBookResult>[
      for (var libraryIndex = 0;
          libraryIndex < injected.length;
          libraryIndex++)
        _ResolvedBookResult(
          book: _ownedTitleAsBook(injected[libraryIndex]),
          ownership: injected[libraryIndex].ownership,
          ownershipStatusKnown: injected[libraryIndex].statusKnown,
          identityAmbiguous: false,
          sourceIdentity: 'library:$libraryIndex',
          cover: injected[libraryIndex].cover.isNotEmpty
              ? injected[libraryIndex].cover
              : null,
          canonicalForeignId: injected[libraryIndex].foreignBookId,
        ),
      ...owned,
      ...rest,
    ];

    if (ordered.isEmpty) {
      return LayoutBuilder(
        builder: (context, constraints) => SingleChildScrollView(
          child: ConstrainedBox(
            constraints: BoxConstraints(minHeight: constraints.maxHeight),
            child: Padding(
              padding: const EdgeInsets.all(32),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  const Icon(Icons.menu_book,
                      size: 48, color: AppTheme.textSecondary),
                  const SizedBox(height: 12),
                  Text(
                    _searched
                        ? 'No books found. Try a different search.'
                        : 'Search for a book or author to request.\nYour library lives in the Books section.',
                    textAlign: TextAlign.center,
                    style: const TextStyle(color: AppTheme.textSecondary),
                  ),
                ],
              ),
            ),
          ),
        ),
      );
    }
    // One RequestService for the whole result list (requests go through the
    // backend's /requests endpoint, not the Chaptarr proxy).
    final requestService =
        RequestService(backendDio: ref.read(backendClientProvider));
    final requestRefreshTick = ref.watch(libraryRefreshTickProvider);
    final instanceId = ref.read(instanceProvider).activeChaptarrInstance?.id;
    // Full-width scroll surface; the result column is capped and centered so
    // rows stay readable on desktop widths.
    return LayoutBuilder(builder: (context, constraints) {
      final hPad = AppBreakpoints.centeredContentPadding(
        constraints.maxWidth,
        minPadding: 0,
      );
      return ListView.separated(
        padding: EdgeInsets.fromLTRB(hPad, 8, hPad, 8),
        itemCount: ordered.length,
        separatorBuilder: (_, __) =>
            const Divider(height: 1, color: AppTheme.border),
        itemBuilder: (_, i) => _BookResultTile(
          book: ordered[i].book,
          canonicalForeignId: ordered[i].canonicalForeignId,
          ownership: ordered[i].ownership,
          ownershipStatusKnown: ordered[i].ownershipStatusKnown,
          identityAmbiguous: ordered[i].identityAmbiguous,
          sourceIdentity: ordered[i].sourceIdentity,
          cover: instanceId == null
              ? null
              : chaptarrImageSource(ref, ordered[i].cover, instanceId),
          requestService: requestService,
          requestRefreshTick: requestRefreshTick,
          instanceId: instanceId,
          onRequestCompleted: _refreshBookTruth,
        ),
      );
    });
  }
}

class _ResolvedBookResult {
  final ChaptarrBook book;
  final BookOwnership? ownership;
  final bool ownershipStatusKnown;
  final bool identityAmbiguous;
  final String sourceIdentity;
  final String? cover;
  final String canonicalForeignId;

  const _ResolvedBookResult({
    required this.book,
    required this.ownership,
    required this.ownershipStatusKnown,
    required this.identityAmbiguous,
    required this.sourceIdentity,
    required this.cover,
    required this.canonicalForeignId,
  });
}

class _BookResultTile extends StatelessWidget {
  final ChaptarrBook book;
  final String canonicalForeignId;
  final BookOwnership? ownership;
  final bool ownershipStatusKnown;
  final bool identityAmbiguous;
  final String sourceIdentity;
  final ChaptarrImageSource? cover;
  final RequestService requestService;
  final int requestRefreshTick;
  final String? instanceId;
  final VoidCallback onRequestCompleted;

  const _BookResultTile({
    required this.book,
    required this.canonicalForeignId,
    this.ownership,
    this.ownershipStatusKnown = true,
    this.identityAmbiguous = false,
    required this.sourceIdentity,
    this.cover,
    required this.requestService,
    required this.requestRefreshTick,
    required this.instanceId,
    required this.onRequestCompleted,
  });

  @override
  Widget build(BuildContext context) {
    final year = book.releaseDate?.year;
    final subtitle = <String>[
      if (book.author?.authorName.isNotEmpty ?? false) book.author!.authorName,
      if (year != null) '$year',
    ].join(' · ');
    // Lookup metadata can use a provider-specific foreign id that differs from
    // the actual library record. Status, navigation, and mutation all stay on
    // the matched canonical library id while [book] preserves lookup metadata.
    final fid = canonicalForeignId.trim();
    final lookupId = book.foreignBookId?.trim() ?? '';
    final o = ownership;
    final chip = _ownershipChip(o);
    final canOpen = fid.isNotEmpty && !identityAmbiguous;
    final identityGuidance = identityAmbiguous
        ? 'Choose a matching library record'
        : fid.isEmpty
            ? 'Ask an admin to check this book’s library record'
            : null;
    final stackAction = MediaQuery.textScalerOf(context).scale(1) > 1.3 ||
        MediaQuery.sizeOf(context).width < 360;
    final requestControl = canOpen
        ? ConstrainedBox(
            constraints: BoxConstraints(maxWidth: stackAction ? 288 : 180),
            child: BookRequestButton(
              key: ValueKey(
                'book-request:$lookupId:$fid:$sourceIdentity:$requestRefreshTick',
              ),
              foreignId: fid,
              title: book.title,
              instanceId: instanceId,
              service: requestService,
              ownership: o,
              ownershipStatusKnown: ownershipStatusKnown,
              refreshTick: requestRefreshTick,
              showCoveredStatus: false,
              onRequestCompleted: onRequestCompleted,
            ),
          )
        : null;

    final resultKey = ValueKey('book-result:$lookupId:$fid:$sourceIdentity');
    final tile = ListTile(
      key: canOpen && stackAction ? null : resultKey,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      isThreeLine: stackAction,
      minVerticalPadding: stackAction ? 12 : null,
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
      subtitle: (subtitle.isEmpty &&
              chip == null &&
              identityGuidance == null)
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
      // The whole row remains an entry point before and after requesting. The
      // chevron makes that destination visible even while the trailing request
      // control is disabled because both formats are already covered.
      trailing: canOpen
          ? stackAction
              ? const Icon(Icons.chevron_right,
                  color: AppTheme.textSecondary)
              : Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                requestControl!,
                const Icon(Icons.chevron_right,
                    color: AppTheme.textSecondary),
              ],
            )
          : null,
      onTap: canOpen
          ? () => context.push(
                '/detail/book/${Uri.encodeComponent(fid)}'
                '?title=${Uri.encodeQueryComponent(book.title)}'
                '${instanceId == null ? '' : '&instance_id=${Uri.encodeQueryComponent(instanceId!)}'}',
                extra: book,
              )
          : null,
    );
    if (!canOpen || !stackAction) return tile;

    // At narrow widths and large text scales, keeping request guidance in the
    // ListTile subtitle constrains it to the small column beside the cover and
    // can transiently overflow while status loads. Give it the full row below
    // the book summary so neither guidance nor format actions are truncated.
    return Column(
      key: resultKey,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        tile,
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
          child: Align(
            alignment: Alignment.centerLeft,
            child: requestControl!,
          ),
        ),
      ],
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
  // must not make the whole group look available while its audiobook is still
  // only monitored.
  final available = (!o.ebook.owned || o.ebook.downloaded) &&
      (!o.audiobook.owned || o.audiobook.downloaded);
  return _OwnershipChip(
    label: states.join(' · '),
    color: available ? AppTheme.available : AppTheme.requested,
  );
}

/// A synthetic result for an owned library title the metadata search didn't
/// return. It carries the owned record's foreignBookId, so a partly-owned title
/// (e.g. ebook present, audiobook missing) still gets a "Request more" button to
/// complete the missing format.
ChaptarrBook _ownedTitleAsBook(OwnedTitle t) => ChaptarrBook(
      id: 0,
      title: t.title,
      foreignBookId: t.foreignBookId.isNotEmpty ? t.foreignBookId : null,
      author: ChaptarrAuthorContext(id: 0, authorName: t.author),
      releaseDate: t.year > 0 ? DateTime(t.year) : null,
    );

/// A small colored pill marking that a search result is already in the library.
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
