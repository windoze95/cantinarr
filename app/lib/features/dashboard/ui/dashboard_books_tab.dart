import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
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

class _DashboardBooksTabState extends ConsumerState<DashboardBooksTab> {
  final _controller = TextEditingController();
  Timer? _debounce;
  List<ChaptarrBook> _results = [];
  bool _isSearching = false;
  bool _searched = false;
  String? _error;
  int _searchGen = 0; // guards against superseded async results
  String _searchedTerm = ''; // term the current _results belong to

  @override
  void dispose() {
    _debounce?.cancel();
    _controller.dispose();
    super.dispose();
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
      setState(() {
        _results = [];
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
        // Surface the real failure: a 404 usually means this Chaptarr build's
        // search API differs from Readarr's /api/v1/book/lookup; 401/403 is an
        // access/grant problem.
        _error = code != null
            ? 'Search failed (HTTP $code). This Chaptarr instance may not support /api/v1/book/lookup.'
            : 'Search failed: ${e.message ?? 'network error'}.';
      });
    } catch (e) {
      if (!mounted || gen != _searchGen) return;
      setState(() {
        _isSearching = false;
        _searched = true;
        _error = 'Search failed: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
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
    // Owned library titles matching the query that lookup didn't return — shown
    // first as "you already have this" rows. They carry no foreignBookId, so
    // they're informational (no Request button).
    final injected = digest.isEmpty
        ? const <OwnedTitle>[]
        : ownedTitlesForQuery(_searchedTerm, digest, _results);
    // Mark each lookup result with its ownership and float owned titles to the
    // top, preserving Chaptarr's relevance order within each bucket (don't
    // collapse versions — the user wants to see ones they don't own). Only owned
    // results carry a cover: the owned record's cached /MediaCover, which loads
    // with the API key. Lookup (/MediaCoverProxy) covers are broken server-side
    // in this fork, so we don't attempt them — not-yet-owned rows stay iconic.
    final owned = <(ChaptarrBook, BookOwnership?, String?)>[];
    final rest = <(ChaptarrBook, BookOwnership?, String?)>[];
    for (final book in _results) {
      final match = digest.isEmpty ? null : ownedMatchFor(book, digest);
      final cover =
          (match != null && match.cover.isNotEmpty) ? match.cover : null;
      ((match?.ownership.anyOwned ?? false) ? owned : rest)
          .add((book, match?.ownership, cover));
    }
    final ordered = <(ChaptarrBook, BookOwnership?, String?)>[
      for (final t in injected)
        (
          _ownedTitleAsBook(t),
          t.ownership,
          t.cover.isNotEmpty ? t.cover : null
        ),
      ...owned,
      ...rest,
    ];

    if (ordered.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
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
          book: ordered[i].$1,
          ownership: ordered[i].$2,
          cover: instanceId == null
              ? null
              : chaptarrImageSource(ref, ordered[i].$3, instanceId),
          requestService: requestService,
          requestRefreshTick: requestRefreshTick,
        ),
      );
    });
  }
}

class _BookResultTile extends StatelessWidget {
  final ChaptarrBook book;
  final BookOwnership? ownership;
  final ChaptarrImageSource? cover;
  final RequestService requestService;
  final int requestRefreshTick;

  const _BookResultTile({
    required this.book,
    this.ownership,
    this.cover,
    required this.requestService,
    required this.requestRefreshTick,
  });

  @override
  Widget build(BuildContext context) {
    final year = book.releaseDate?.year;
    final subtitle = <String>[
      if (book.author?.authorName.isNotEmpty ?? false) book.author!.authorName,
      if (year != null) '$year',
    ].join(' · ');
    final fid = book.foreignBookId;
    final o = ownership;
    // Lookup results never carry a library id (always null), so ownership comes
    // from the owned-books digest, matched by title+author. Only hide the request
    // affordance when BOTH files are present — a format that's missing or merely
    // monitored can still be requested.
    final bothDownloaded =
        o != null && o.ebook.downloaded && o.audiobook.downloaded;
    final chip = _ownershipChip(o);
    final canOpen = fid != null && fid.isNotEmpty;

    return ListTile(
      key: ValueKey('book-result:$fid'),
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
      subtitle: (subtitle.isEmpty && chip == null)
          ? null
          : Padding(
              padding: const EdgeInsets.only(top: 3),
              child: Row(
                children: [
                  if (subtitle.isNotEmpty)
                    Flexible(
                      child: Text(subtitle,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style:
                              const TextStyle(color: AppTheme.textSecondary)),
                    ),
                  if (chip != null) ...[
                    if (subtitle.isNotEmpty) const SizedBox(width: 8),
                    chip,
                  ],
                ],
              ),
            ),
      // The whole row remains an entry point before and after requesting. The
      // chevron makes that destination visible even while the trailing request
      // control is disabled because both formats are already covered.
      trailing: canOpen
          ? Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (!bothDownloaded)
                  BookRequestButton(
                    key: ValueKey('book-request:$fid:$requestRefreshTick'),
                    foreignId: fid,
                    title: book.title,
                    service: requestService,
                    ownership: o,
                  ),
                const Icon(Icons.chevron_right,
                    color: AppTheme.textSecondary),
              ],
            )
          : null,
      onTap: canOpen
          ? () => context.push(
                '/detail/book/${Uri.encodeComponent(fid)}'
                '?title=${Uri.encodeQueryComponent(book.title)}',
                extra: book,
              )
          : null,
    );
  }
}

Widget? _ownershipChip(BookOwnership? o) {
  if (o == null || !o.anyOwned) return null;
  final downloaded = o.anyDownloaded;
  return _OwnershipChip(
    label: downloaded ? 'Downloaded' : 'In Library',
    color: downloaded ? AppTheme.available : AppTheme.requested,
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
