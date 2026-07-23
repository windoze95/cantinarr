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
import '../logic/book_search_ranking.dart';
import 'book_request_wizard.dart';

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
  static const _maxPrefixFallbacks = 8;

  final _controller = TextEditingController();
  Timer? _debounce;
  List<ChaptarrBook> _results = [];
  bool _isSearching = false;
  bool _searched = false;
  String? _error;
  int _searchGen = 0; // guards against superseded async results
  String _searchedTerm = ''; // full user query the current _results satisfy
  String _resultLookupTerm = ''; // exact or fallback term that returned them
  final Map<String, BookRequestStatusDetail> _requestDetails = {};

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
    _debounce?.cancel();
    final term = _controller.text.trim();
    final gen = ++_searchGen;
    if (term.isEmpty) {
      setState(() {
        _results = [];
        _searchedTerm = '';
        _resultLookupTerm = '';
        _isSearching = false;
        _searched = false;
        _error = null;
      });
      return;
    }
    setState(() {
      _isSearching = true;
      _error = null;
    });
    _debounce = Timer(
      const Duration(milliseconds: 400),
      () => _search(term, gen),
    );
  }

  Future<void> _searchNow() {
    _debounce?.cancel();
    final term = _controller.text.trim();
    return _search(term, ++_searchGen);
  }

  ChaptarrApiService? _chaptarr() {
    final instance = ref.read(instanceProvider).activeChaptarrInstance;
    if (instance == null) return null;
    return ChaptarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instance.id,
    );
  }

  Future<void> _search(
    String term,
    int gen, {
    bool showLoading = true,
  }) async {
    if (!mounted || gen != _searchGen) return;
    if (term.isEmpty) {
      setState(() {
        _results = [];
        _searchedTerm = '';
        _resultLookupTerm = '';
        _isSearching = false;
        _searched = false;
        _error = null;
      });
      return;
    }
    final service = _chaptarr();
    if (service == null) {
      setState(() {
        _isSearching = false;
        _searched = true;
        _error = 'No Chaptarr instance is available.';
      });
      return;
    }
    setState(() {
      _isSearching = showLoading;
      _error = null;
    });
    try {
      var books = await service.lookupBook(term);
      if (!mounted || gen != _searchGen) return;
      var resultLookupTerm = term;
      if (books.isEmpty) {
        var previousTerm = term;
        for (var attempt = 0;
            attempt < _maxPrefixFallbacks;
            attempt++) {
          final fallbackTerm = bookSearchPrefixFallbackTerm(previousTerm);
          if (fallbackTerm == null) break;
          previousTerm = fallbackTerm;
          final fallbackBooks = await service.lookupBook(fallbackTerm);
          if (!mounted || gen != _searchGen) return;
          final matchingBooks = fallbackBooks
              .where((book) => stronglyMatchesBookSearch(term, book))
              .toList();
          if (matchingBooks.isEmpty) continue;
          books = matchingBooks;
          resultLookupTerm = fallbackTerm;
          break;
        }
      }
      setState(() {
        _results = books;
        _searchedTerm = term;
        _resultLookupTerm = resultLookupTerm;
        _requestDetails.clear();
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
            _resultLookupTerm = '';
            _searched = false;
            _isSearching = false;
            _error = null;
            _requestDetails.clear();
          });
          ref.invalidate(ownedBooksProvider);
          if (_controller.text.trim().isNotEmpty) _searchNow();
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
            onSubmitted: (_) => _searchNow(),
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
                        _onChanged();
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
    // lookup mapping. Every distinct record stays visible; the request wizard
    // handles equivalent backend copies without asking for internal IDs.
    final injected = digest.isEmpty
        ? const <OwnedTitle>[]
        : ownedTitlesForQuery(_searchedTerm, digest, _results);
    // Mark each lookup result with its ownership. Only owned results carry a
    // cover: the owned record's cached /MediaCover, which loads with the API
    // key. Lookup (/MediaCoverProxy) covers are broken server-side in this fork,
    // so we don't attempt them — not-yet-owned rows stay iconic.
    final safeMatches = unambiguousOwnedMatches(_results, digest);
    final lookupResults = <_ResolvedBookResult>[];
    for (var lookupIndex = 0;
        lookupIndex < _results.length;
        lookupIndex++) {
      final book = _results[lookupIndex];
      // Only a safe one-to-one identity may borrow library ownership. An
      // ambiguous lookup keeps its own provider id and loads its own status;
      // concrete library rows remain independently visible and actionable.
      final match = safeMatches[book];
      final cover = (match != null && match.cover.isNotEmpty)
          ? match.cover
          : null;
      final libraryId = match?.foreignBookId.trim() ?? '';
      final lookupId = book.foreignBookId?.trim() ?? '';
      lookupResults.add(_ResolvedBookResult(
        book: book,
        ownership: match?.ownership,
        ownershipStatusKnown: match?.statusKnown ?? true,
        sourceIdentity: 'lookup:$lookupIndex',
        lookupTerm: _resultLookupTerm,
        catalogForeignBookId: lookupId.isEmpty ? null : lookupId,
        cover: cover,
        canonicalForeignId: libraryId.isNotEmpty ? libraryId : lookupId,
      ));
    }
    final resolved = <_ResolvedBookResult>[
      for (var libraryIndex = 0;
          libraryIndex < injected.length;
          libraryIndex++)
        _ResolvedBookResult(
          book: _ownedTitleAsBook(injected[libraryIndex]),
          ownership: injected[libraryIndex].ownership,
          ownershipStatusKnown: injected[libraryIndex].statusKnown,
          sourceIdentity: 'library:$libraryIndex',
          lookupTerm: _resultLookupTerm,
          cover: injected[libraryIndex].cover.isNotEmpty
              ? injected[libraryIndex].cover
              : null,
          canonicalForeignId: injected[libraryIndex].foreignBookId,
        ),
      ...lookupResults,
    ];
    final ranked = rankBookSearchResults(
      _searchedTerm,
      resolved.map((result) => result.book).toList(),
    );
    final ordered = [
      for (final result in ranked)
        _RankedResolvedBookResult(
          result: resolved[result.originalIndex],
          rank: result,
        ),
    ];
    final recommendedIndex = ordered.indexWhere((candidate) =>
        candidate.rank.recommendationEligible &&
        candidate.result.canonicalForeignId.trim().isNotEmpty);

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
          book: ordered[i].result.book,
          canonicalForeignId: ordered[i].result.canonicalForeignId,
          lookupTerm: ordered[i].result.lookupTerm,
          catalogForeignBookId:
              ordered[i].result.catalogForeignBookId,
          ownership: ordered[i].result.ownership,
          ownershipStatusKnown: ordered[i].result.ownershipStatusKnown,
          sourceIdentity: ordered[i].result.sourceIdentity,
          recommended: i == recommendedIndex,
          recommendationEvidence: ordered[i].rank.evidence,
          cover: instanceId == null
              ? null
              : chaptarrImageSource(
                  ref,
                  ordered[i].result.cover,
                  instanceId,
                ),
          requestService: requestService,
          requestRefreshTick: requestRefreshTick,
          instanceId: instanceId,
          statusDetail: _requestDetails[_requestDetailKey(
            instanceId,
            ordered[i].result.canonicalForeignId,
          )],
          onDetailChanged: (detail) {
            if (!mounted) return;
            setState(() {
              _requestDetails[_requestDetailKey(
                instanceId,
                ordered[i].result.canonicalForeignId,
              )] = detail;
            });
          },
          requestTargetPicker: (context, request) {
            _requestDetails[_requestDetailKey(
              instanceId,
              request.foreignId,
            )] = request.detail;
            return showBookRequestWizard(
              context,
              request: request,
              selectedBook: ordered[i].result.book,
              candidates: [
                for (var candidateIndex = 0;
                    candidateIndex < ordered.length;
                    candidateIndex++)
                  BookRequestWizardCandidate(
                    book: ordered[candidateIndex].result.book,
                    foreignId:
                        ordered[candidateIndex].result.canonicalForeignId,
                    lookupTerm:
                        ordered[candidateIndex].result.lookupTerm,
                    catalogForeignBookId:
                        ordered[candidateIndex]
                            .result
                            .catalogForeignBookId,
                    ownership: ordered[candidateIndex].result.ownership,
                    ownershipStatusKnown:
                        ordered[candidateIndex].result.ownershipStatusKnown,
                    statusDetail: _requestDetails[_requestDetailKey(
                      instanceId,
                      ordered[candidateIndex].result.canonicalForeignId,
                    )],
                    rank: candidateIndex,
                    matchEvidence: ordered[candidateIndex].rank.evidence,
                    recommendationEligible:
                        ordered[candidateIndex].rank.recommendationEligible,
                  ),
              ],
            );
          },
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
  final String sourceIdentity;
  final String lookupTerm;
  final String? catalogForeignBookId;
  final String? cover;
  final String canonicalForeignId;

  const _ResolvedBookResult({
    required this.book,
    required this.ownership,
    required this.ownershipStatusKnown,
    required this.sourceIdentity,
    required this.lookupTerm,
    this.catalogForeignBookId,
    required this.cover,
    required this.canonicalForeignId,
  });
}

class _RankedResolvedBookResult {
  final _ResolvedBookResult result;
  final RankedBookSearchResult rank;

  const _RankedResolvedBookResult({
    required this.result,
    required this.rank,
  });
}

class _BookResultTile extends StatelessWidget {
  final ChaptarrBook book;
  final String canonicalForeignId;
  final String lookupTerm;
  final String? catalogForeignBookId;
  final BookOwnership? ownership;
  final bool ownershipStatusKnown;
  final String sourceIdentity;
  final bool recommended;
  final String recommendationEvidence;
  final ChaptarrImageSource? cover;
  final RequestService requestService;
  final int requestRefreshTick;
  final String? instanceId;
  final BookRequestStatusDetail? statusDetail;
  final ValueChanged<BookRequestStatusDetail> onDetailChanged;
  final BookRequestTargetPicker requestTargetPicker;
  final VoidCallback onRequestCompleted;

  const _BookResultTile({
    required this.book,
    required this.canonicalForeignId,
    required this.lookupTerm,
    this.catalogForeignBookId,
    this.ownership,
    this.ownershipStatusKnown = true,
    required this.sourceIdentity,
    this.recommended = false,
    this.recommendationEvidence = '',
    this.cover,
    required this.requestService,
    required this.requestRefreshTick,
    required this.instanceId,
    required this.statusDetail,
    required this.onDetailChanged,
    required this.requestTargetPicker,
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
    final catalogId = catalogForeignBookId?.trim() ?? '';
    final o = ownership;
    final chip = _ownershipChip(o, statusDetail);
    final canOpen = fid.isNotEmpty;
    final identityGuidance = fid.isEmpty
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
              onDetailChanged: onDetailChanged,
              requestTargetPicker: requestTargetPicker,
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
      subtitle: (!recommended &&
              subtitle.isEmpty &&
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
                  if (recommended) ...[
                    if (subtitle.isNotEmpty) const SizedBox(height: 5),
                    _SearchRecommendation(
                      evidence: recommendationEvidence,
                    ),
                  ],
                  if (identityGuidance != null) ...[
                    if (subtitle.isNotEmpty || recommended)
                      const SizedBox(height: 4),
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
                    if (subtitle.isNotEmpty ||
                        recommended ||
                        identityGuidance != null)
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
                '&lookup_term=${Uri.encodeQueryComponent(lookupTerm)}'
                '${catalogId.isEmpty ? '' : '&catalog_foreign_book_id=${Uri.encodeQueryComponent(catalogId)}'}'
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

Widget? _ownershipChip(
  BookOwnership? ownership,
  BookRequestStatusDetail? detail,
) {
  final states = <({String label, RequestStatus status})>[];
  void addFormat(
    String label,
    BookRequestFormat format,
    FormatOwnership? owned,
  ) {
    final status = (owned?.downloaded ?? false)
        ? RequestStatus.available
        : detail?.formats[format];
    final suffix = switch (status) {
      RequestStatus.available => 'available',
      RequestStatus.downloading => 'downloading',
      RequestStatus.requested || RequestStatus.partial => 'requested',
      RequestStatus.pending => 'pending approval',
      RequestStatus.denied || RequestStatus.unavailable || null => null,
    };
    if (suffix != null && status != null) {
      states.add((label: '$label $suffix', status: status));
    }
  }

  addFormat(
    'eBook',
    BookRequestFormat.ebook,
    ownership?.ebook,
  );
  addFormat(
    'Audiobook',
    BookRequestFormat.audiobook,
    ownership?.audiobook,
  );
  if (states.isEmpty) return null;
  final color = states.any((state) => state.status == RequestStatus.downloading)
      ? AppTheme.downloading
      : states.every((state) => state.status == RequestStatus.available)
          ? AppTheme.available
          : AppTheme.requested;
  return _OwnershipChip(
    label: states.map((state) => state.label).join(' · '),
    color: color,
  );
}

class _SearchRecommendation extends StatelessWidget {
  final String evidence;

  const _SearchRecommendation({required this.evidence});

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: 6,
      runSpacing: 4,
      crossAxisAlignment: WrapCrossAlignment.center,
      children: [
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 3),
          decoration: BoxDecoration(
            color: AppTheme.accent.withValues(alpha: 0.16),
            borderRadius: BorderRadius.circular(AppTheme.radiusPill),
          ),
          child: const Text(
            'Closest match',
            style: TextStyle(
              color: AppTheme.accent,
              fontSize: 10.5,
              fontWeight: FontWeight.w800,
            ),
          ),
        ),
        if (evidence.isNotEmpty)
          Text(
            evidence,
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 11,
              fontWeight: FontWeight.w600,
            ),
          ),
      ],
    );
  }
}

String _requestDetailKey(String? instanceId, String foreignId) =>
    '${instanceId ?? ''}\u0000${foreignId.trim()}';

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
