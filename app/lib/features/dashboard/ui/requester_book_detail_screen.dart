import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../navigation/ambient_page_route.dart';
import '../../auth/logic/auth_provider.dart';
import '../../chaptarr/data/chaptarr_api_service.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../../chaptarr/data/chaptarr_models.dart';
import '../../chaptarr/ui/chaptarr_book_screen.dart';
import '../../issues/ui/report_problem_sheet.dart';
import '../../media_download/data/media_download_models.dart';
import '../../media_download/ui/media_download_button.dart';
import '../../request/data/book_ownership.dart';
import '../../request/data/request_service.dart';
import '../../request/ui/book_format_panel.dart';
import '../data/book_library_service.dart';
import '../logic/book_ownership_matcher.dart';

/// Requester-facing detail for one book, addressed by its Chaptarr/Readarr
/// foreignBookId. Search navigation supplies [initialBook] for an immediate,
/// metadata-rich presentation; notification/deep links resolve the same data
/// from the title hint when possible, and the owned-books digest remains the
/// live source of per-format ownership.
class RequesterBookDetailScreen extends ConsumerStatefulWidget {
  final String foreignId;
  final String? titleHint;
  final ChaptarrBook? initialBook;
  final String? instanceId;

  /// The term the requester searched to reach this book, when they arrived from
  /// search. Requesting an untracked book makes the server find this exact
  /// metadata record again, and this is the search already proven to return it.
  final String? searchTerm;

  const RequesterBookDetailScreen({
    super.key,
    required this.foreignId,
    this.titleHint,
    this.initialBook,
    this.instanceId,
    this.searchTerm,
  });

  @override
  ConsumerState<RequesterBookDetailScreen> createState() =>
      _RequesterBookDetailScreenState();
}

class _RequesterBookDetailScreenState
    extends ConsumerState<RequesterBookDetailScreen>
    with WidgetsBindingObserver {
  late final RequestService _requestService;
  ChaptarrBook? _metadata;
  List<ChaptarrBook> _chaptarrRecords = const [];
  Map<int, List<ChaptarrBookFile>> _filesByBook = const {};
  bool _metadataLoading = false;
  int _loadGeneration = 0;
  int _recordsLoadGeneration = 0;
  String? _instanceId;

  /// The foreignBookId the library files this book under, when the server
  /// reported it differs from [widget.foreignId] (Chaptarr re-keys created
  /// records to its own canonical ids). Ownership binding, live records, and
  /// the format panel all follow this id once known.
  String? _canonicalForeignId;

  String get _effectiveForeignId => _canonicalForeignId ?? widget.foreignId;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _requestService =
        RequestService(backendDio: ref.read(backendClientProvider));
    _startLoads();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _refreshBookTruth();
  }

  @override
  void didUpdateWidget(covariant RequesterBookDetailScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.foreignId != widget.foreignId ||
        oldWidget.initialBook != widget.initialBook ||
        oldWidget.titleHint != widget.titleHint ||
        oldWidget.instanceId != widget.instanceId) {
      _startLoads();
    }
  }

  void _startLoads() {
    final generation = ++_loadGeneration;
    _recordsLoadGeneration++;
    _instanceId = widget.instanceId ??
        ref.read(instanceProvider).activeChaptarrInstance?.id;
    _metadata = widget.initialBook;
    _chaptarrRecords = const [];
    _filesByBook = const {};
    _canonicalForeignId = null;
    _metadataLoading = widget.initialBook == null &&
        (widget.titleHint?.trim().isNotEmpty ?? false);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _resolveMetadata(generation);
      _resolveChaptarrRecords(generation);
    });
  }

  ChaptarrApiService? _chaptarrService() {
    final instanceId = _instanceId;
    if (instanceId == null) return null;
    return ChaptarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  /// Notification links carry only a title and foreign id. Resolve their
  /// metadata with the same read-only lookup as Books search. Prefer the exact
  /// foreign id. An older provider-id mismatch may use metadata only when the
  /// canonical digest row exists and exactly one lookup result strongly
  /// matches both that row's title and author.
  Future<void> _resolveMetadata(int generation) async {
    if (_metadata != null) return;
    final term = widget.titleHint?.trim() ?? '';
    final service = _chaptarrService();
    if (term.isEmpty || service == null) {
      if (mounted && generation == _loadGeneration) {
        setState(() => _metadataLoading = false);
      }
      return;
    }
    ChaptarrBook? match;
    try {
      final results = await service.lookupBook(term);
      for (final book in results) {
        if (book.foreignBookId == widget.foreignId) {
          match = book;
          break;
        }
      }
      if (match == null) {
        final digest = await ref.read(
          ownedBooksForInstanceProvider(_instanceId).future,
        );
        final canonicalRows = digest
            .where((owned) =>
                owned.foreignBookId.trim() == widget.foreignId.trim())
            .toList(growable: false);
        if (canonicalRows.length == 1) {
          final canonical = canonicalRows.single;
          final strongIdentityMatches = results
              .where((book) =>
                  strongNormalizedTitleMatch(book.title, canonical.title) &&
                  strongAuthorMatch(
                    book.author?.authorName,
                    canonical.author,
                  ))
              .toList(growable: false);
          if (strongIdentityMatches.length == 1) {
            match = strongIdentityMatches.single;
          }
        }
      }
    } catch (_) {
      // The title hint still gives the requester a useful fallback.
    }
    if (!mounted || generation != _loadGeneration) return;
    setState(() {
      _metadata = match;
      _metadataLoading = false;
    });
  }

  /// Resolve exact live Chaptarr records. Lookup records and the requester
  /// digest intentionally lack trustworthy numeric library/file ids, so only
  /// this live list may back admin navigation or requester downloads.
  Future<void> _resolveChaptarrRecords(int generation) async {
    final auth = ref.read(authProvider).valueOrNull;
    final isAdmin = auth?.user?.isAdmin ?? false;
    final downloadsEnabled =
        auth?.connection?.mediaDownloadsEnabledFor(_instanceId) ?? false;
    if (!isAdmin && !downloadsEnabled) return;
    final service = _chaptarrService();
    if (service == null) return;
    final recordsGeneration = ++_recordsLoadGeneration;
    try {
      final books = await service.getBooks();
      final matches = books
          .where((book) =>
              book.id > 0 && book.foreignBookId == _effectiveForeignId)
          .toList(growable: false)
        ..sort((a, b) => a.format.index.compareTo(b.format.index));
      final filesByBook = <int, List<ChaptarrBookFile>>{};
      if (downloadsEnabled) {
        final results = await Future.wait(matches.map((book) async {
          try {
            return await service.getBookFiles(bookId: book.id);
          } catch (_) {
            return const <ChaptarrBookFile>[];
          }
        }));
        for (var i = 0; i < matches.length; i++) {
          filesByBook[matches[i].id] = results[i]
              .where((file) => file.id > 0)
              .toList(growable: false);
        }
      }
      if (!mounted ||
          generation != _loadGeneration ||
          recordsGeneration != _recordsLoadGeneration) {
        return;
      }
      setState(() {
        _chaptarrRecords = matches;
        _filesByBook = filesByBook;
      });
    } catch (_) {
      // A transient Chaptarr failure keeps the optional link hidden.
    }
  }

  /// Follows the library's canonical id once the server reports one: the
  /// digest row can then bind, live records resolve, and the format panel
  /// re-keys onto the id every future read will agree on.
  void _onCanonicalForeignId(String canonical) {
    if (!mounted || canonical.isEmpty || canonical == _effectiveForeignId) {
      return;
    }
    setState(() => _canonicalForeignId = canonical);
    _resolveChaptarrRecords(_loadGeneration);
  }

  Future<void> _onRequestCompleted() async {
    // The request may have created the live Chaptarr records immediately.
    // Refresh both the ownership digest and the admin destination in place.
    ref.invalidate(ownedBooksForInstanceProvider(_instanceId));
    ref.read(libraryRefreshTickProvider.notifier).state++;
    await _resolveChaptarrRecords(_loadGeneration);
  }

  Future<void> _refreshBookTruth() async {
    ref.invalidate(ownedBooksForInstanceProvider(_instanceId));
    ref.read(libraryRefreshTickProvider.notifier).state++;
    await _resolveChaptarrRecords(_loadGeneration);
  }

  /// The formats a reporter can flag. Live Chaptarr records are the richest
  /// truth but resolve only for admins/download-enabled users; plain requesters
  /// fall back to the requester-visible ownership digest (owned = monitored or
  /// downloaded, which includes a stuck download — exactly what reports are
  /// for), failing closed while the digest is still unknown.
  List<BookFormat> _reportableFormats(OwnedTitle? owned) {
    final live = _chaptarrRecords
        .where((record) => record.id > 0)
        .map((record) => record.format)
        .where((format) => format != BookFormat.unknown)
        .toSet()
        .toList();
    if (live.isNotEmpty) return live;
    if (owned == null || !owned.statusKnown) return const [];
    return [
      if (owned.ownership.ebook.owned) BookFormat.ebook,
      if (owned.ownership.audiobook.owned) BookFormat.audiobook,
    ];
  }

  /// A book is reportable once the library tracks it in some form (a live
  /// Chaptarr record, or an owned format in the requester digest) and the
  /// server allows reporting — mirroring the movie/TV gate, which also derives
  /// from requester-visible library truth.
  bool _canReportBook(OwnedTitle? owned) {
    final allow = ref
            .watch(authProvider)
            .valueOrNull
            ?.connection
            ?.allowReporting ??
        false;
    if (!allow || _instanceId == null) return false;
    if (_chaptarrRecords.any((record) => record.id > 0)) return true;
    return _reportableFormats(owned).isNotEmpty;
  }

  /// Opens the report flow. An ebook and audiobook of the same title are
  /// distinct Chaptarr records, so when both exist the reporter picks which
  /// format the problem is about — never a silent merge. With no format
  /// knowledge at all the report goes up format-less and the server resolves
  /// (or asks for the format via its ambiguity error).
  Future<void> _onReportProblem(String title, OwnedTitle? owned) async {
    final instanceId = _instanceId;
    if (instanceId == null) return;
    final formats = _reportableFormats(owned);
    String? format;
    if (formats.length == 1) {
      format = formats.first == BookFormat.audiobook ? 'audiobook' : 'ebook';
    } else if (formats.length > 1) {
      final picked = await showAppSheet<BookFormat>(
        context,
        builder: (sheetContext) => AppSheet(
          padding: EdgeInsets.zero,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Padding(
                padding: EdgeInsets.fromLTRB(24, 0, 24, 6),
                child: Text('Which format is the problem with?'),
              ),
              ListTile(
                leading: const Icon(Icons.menu_book_outlined),
                title: const Text('eBook'),
                onTap: () => Navigator.of(sheetContext).pop(BookFormat.ebook),
              ),
              ListTile(
                leading: const Icon(Icons.headphones_outlined),
                title: const Text('Audiobook'),
                onTap: () =>
                    Navigator.of(sheetContext).pop(BookFormat.audiobook),
              ),
            ],
          ),
        ),
      );
      if (picked == null) return;
      format = picked == BookFormat.audiobook ? 'audiobook' : 'ebook';
    }
    if (!mounted) return;
    await showReportProblemSheet(
      context,
      scope: ReportScope.book(
        instanceId: instanceId,
        foreignId: _effectiveForeignId,
        format: format,
        title: title,
      ),
      onSubmitted: _refreshBookTruth,
    );
  }

  Future<void> _openInChaptarr() async {
    if (_chaptarrRecords.isEmpty) return;
    final instanceId = _instanceId;
    if (instanceId == null) return;
    await Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => ChaptarrBookScreen(
          instanceId: instanceId,
          records: _chaptarrRecords,
          bookTitle: _chaptarrRecords.first.title,
        ),
      ),
    );
    if (mounted) await _refreshBookTruth();
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.hasValue) _refreshBookTruth();
    });
    // A config refresh can enable downloads for this exact Chaptarr instance
    // while the detail page remains open. Re-resolve its live file records so
    // the newly available actions do not require reopening the screen.
    ref.listen(authProvider, (_, __) {
      _resolveChaptarrRecords(_loadGeneration);
    });
    ref.listen(
      instanceProvider.select((state) => state.activeChaptarrInstance?.id),
      (previous, next) {
        if (previous == next || widget.instanceId != null) return;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) setState(_startLoads);
        });
      },
    );
    final digest = ref.watch(ownedBooksForInstanceProvider(_instanceId));
    return Scaffold(
      appBar: AppBar(title: const Text('Book details')),
      // Metadata renders immediately; ownership and request truth resolve in
      // their own rows instead of blanking the whole page behind one digest.
      body: _resolved(digest.valueOrNull ?? const []),
    );
  }

  /// Library titles this page's book may duplicate: fuzzy title/author matches
  /// with some owned format, minus the page's own record. The metadata catalog
  /// keeps duplicate listings for one work, so a requester can land on the
  /// listing their library doesn't use — this is the plain "you may already
  /// have this" pointer that keeps them from requesting the book twice.
  List<OwnedTitle> _libraryLookalikes(
    List<OwnedTitle> titles,
    String title,
    String author,
  ) {
    final probe = ChaptarrBook(
      id: 0,
      title: title,
      author: author.isEmpty
          ? null
          : ChaptarrAuthorContext(id: 0, authorName: author),
    );
    return ownedMatchesFor(probe, titles)
        .where((t) =>
            t.ownership.anyOwned &&
            t.foreignBookId.trim().isNotEmpty &&
            t.foreignBookId.trim() != _effectiveForeignId)
        .toList(growable: false);
  }

  void _openLookalike(OwnedTitle candidate) {
    final instanceId = _instanceId;
    context.push(
      '/detail/book/${Uri.encodeComponent(candidate.foreignBookId.trim())}'
      '?title=${Uri.encodeQueryComponent(candidate.title)}'
      '${instanceId == null ? '' : '&instance_id=${Uri.encodeQueryComponent(instanceId)}'}',
    );
  }

  Widget _resolved(List<OwnedTitle> titles) {
    OwnedTitle? owned;
    for (final title in titles) {
      if (title.foreignBookId.isNotEmpty &&
          title.foreignBookId == _effectiveForeignId) {
        owned = title;
        break;
      }
    }

    final live = _chaptarrRecords.isEmpty ? null : _chaptarrRecords.first;
    final hintedTitle = widget.titleHint?.trim() ?? '';
    final title = _firstText([
      _metadata?.title,
      live?.title,
      owned?.title,
      hintedTitle,
    ]);
    if (title.isEmpty) {
      return _metadataLoading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent),
            )
          : _notFound();
    }

    final author = _firstText([
      _metadata?.author?.authorName,
      live?.author?.authorName,
      owned?.author,
    ]);
    final releaseDate = _metadata?.releaseDate ?? live?.releaseDate;
    final year = releaseDate?.year ?? owned?.year ?? 0;
    final overview = _firstText([
      _metadata?.displayOverview,
      live?.displayOverview,
    ]);
    final metadataPageCount = _metadata?.displayPageCount ?? 0;
    final pageCount = metadataPageCount > 0
        ? metadataPageCount
        : (live?.displayPageCount ?? 0);
    final genres = _metadata?.genres.isNotEmpty ?? false
        ? _metadata!.genres
        : (live?.genres ?? const <String>[]);
    final ownership = owned?.ownership;
    // Only a page that could not bind to its own library record needs the
    // pointer; a bound page's format panel already tells the whole truth.
    final lookalikes = owned != null
        ? const <OwnedTitle>[]
        : _libraryLookalikes(titles, title, author);
    final auth = ref.watch(authProvider).valueOrNull;
    final instanceId = _instanceId;
    final downloadsEnabled =
        auth?.connection?.mediaDownloadsEnabledFor(instanceId) ?? false;
    final ebookFiles = _downloadChoicesFor(BookFormat.ebook);
    final audiobookFiles = _downloadChoicesFor(BookFormat.audiobook);
    final isAdmin = auth?.user?.isAdmin ?? false;

    final requestRefreshTick = ref.watch(libraryRefreshTickProvider);
    ChaptarrImageSource? cover;
    if (instanceId != null) {
      final rawOwnedCover = owned?.cover.trim() ?? '';
      final ownedCover = rawOwnedCover.toLowerCase().startsWith('http')
          ? ''
          : rawOwnedCover;
      final remoteCover = _firstText([
        _metadata?.remoteCoverUrl,
        live?.remoteCoverUrl,
      ]);
      // Live Chaptarr book covers are safe only when relative and routed back
      // through Cantinarr. An absolute arr-origin URL is never surfaced.
      final liveCover = live?.coverUrl ?? '';
      final safeLiveCover = liveCover.toLowerCase().startsWith('http')
          ? ''
          : liveCover;
      cover = chaptarrImageSource(
        ref,
        _firstText([ownedCover, remoteCover, safeLiveCover]),
        instanceId,
      );
    }

    return CenteredContent(
      child: ListView(
        // Build the format panel even when large accessibility text pushes it
        // just below the viewport; it owns this book's live request state.
        cacheExtent: MediaQuery.sizeOf(context).height * 2,
        padding: const EdgeInsets.fromLTRB(24, 20, 24, 32),
        children: [
          Center(
            child: ClipRRect(
              borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
              child: CachedImage(
                url: cover?.url,
                headers: cover?.headers,
                width: 132,
                height: 198,
                icon: Icons.menu_book,
                iconSize: 36,
              ),
            ),
          ),
          const SizedBox(height: 20),
          Semantics(
            header: true,
            child: Text(
              title,
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.headlineSmall,
            ),
          ),
          if (author.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              author,
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodyLarge?.copyWith(
                    color: AppTheme.textSecondary,
                  ),
            ),
          ],
          if (year > 0 || pageCount > 0) ...[
            const SizedBox(height: 6),
            Text(
              [
                if (year > 0) '$year',
                if (pageCount > 0) '$pageCount pages',
              ].join(' · '),
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
          const SizedBox(height: 24),
          if (lookalikes.isNotEmpty) ...[
            _LookalikeNotice(
              candidates: lookalikes,
              onOpen: _openLookalike,
            ),
            const SizedBox(height: 14),
          ],
          BookFormatPanel(
            foreignId: _effectiveForeignId,
            title: title,
            instanceId: instanceId,
            searchTerm: widget.searchTerm,
            service: _requestService,
            ownership: ownership,
            ownershipStatusKnown: owned?.statusKnown ?? true,
            refreshTick: requestRefreshTick,
            onCanonicalForeignId: _onCanonicalForeignId,
            ebookDownload: !downloadsEnabled ||
                    instanceId == null ||
                    ebookFiles.isEmpty
                ? null
                : MediaDownloadChoiceButton(
                    instanceId: instanceId,
                    choices: ebookFiles,
                    label: 'Download eBook',
                    sheetTitle: 'Download eBook',
                    iconOnly: true,
                  ),
            audiobookDownload: !downloadsEnabled ||
                    instanceId == null ||
                    audiobookFiles.isEmpty
                ? null
                : MediaDownloadChoiceButton(
                    instanceId: instanceId,
                    choices: audiobookFiles,
                    label: 'Download audiobook',
                    sheetTitle: 'Download audiobook',
                    iconOnly: true,
                  ),
            onRequestCompleted: _onRequestCompleted,
          ),
          if (_canReportBook(owned)) ...[
            const SizedBox(height: 18),
            // Mirrors the shared ReportProblemButton, but routes through the
            // format picker: an ebook and audiobook are distinct records.
            OutlinedButton.icon(
              onPressed: () => _onReportProblem(title, owned),
              icon: const Icon(Icons.flag_outlined,
                  size: 18, color: AppTheme.textSecondary),
              label: const Text('Report a problem',
                  style: TextStyle(color: AppTheme.textPrimary)),
              style: OutlinedButton.styleFrom(
                side: const BorderSide(color: AppTheme.border),
                shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(10)),
                padding: const EdgeInsets.symmetric(vertical: 12),
              ),
            ),
          ],
          if (isAdmin && _chaptarrRecords.isNotEmpty) ...[
            const SizedBox(height: 18),
            Center(
              child: OutlinedButton.icon(
                onPressed: _openInChaptarr,
                icon: const Icon(Icons.open_in_new_rounded, size: 17),
                label: const Text('Manage book'),
              ),
            ),
          ],
          if (genres.isNotEmpty) ...[
            const SizedBox(height: 22),
            Wrap(
              alignment: WrapAlignment.center,
              spacing: 6,
              runSpacing: 6,
              children: genres
                  .map((genre) => Chip(
                        label: Text(genre),
                        backgroundColor: AppTheme.surfaceVariant,
                        side: const BorderSide(color: AppTheme.border),
                        materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                        visualDensity: VisualDensity.compact,
                      ))
                  .toList(),
            ),
          ],
          if (overview.isNotEmpty) ...[
            const SizedBox(height: 24),
            Text('About this book',
                style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 8),
            Text(
              overview,
              style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                    color: AppTheme.textPrimary,
                  ),
            ),
          ],
        ],
      ),
    );
  }

  List<MediaDownloadChoice> _downloadChoicesFor(BookFormat format) {
    final choices = <MediaDownloadChoice>[];
    for (final record in _chaptarrRecords) {
      if (record.format != format) continue;
      final files = _filesByBook[record.id] ?? const [];
      for (var i = 0; i < files.length; i++) {
        final file = files[i];
        final details = [
          if (file.qualityName?.isNotEmpty ?? false) file.qualityName!,
          if (file.size > 0) file.sizeFormatted,
        ].join(' · ');
        choices.add(MediaDownloadChoice(
          fileId: file.id,
          label: _bookFileLabel(file, i),
          subtitle: details.isEmpty ? null : details,
          reportedPath: file.path,
        ));
      }
    }
    return choices;
  }

  Widget _notFound() {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.menu_book,
                size: 48, color: AppTheme.textSecondary),
            const SizedBox(height: 12),
            const Text(
              'This book could not be found. It may have been removed from '
              'the library.',
              textAlign: TextAlign.center,
              style: TextStyle(color: AppTheme.textSecondary),
            ),
            const SizedBox(height: 16),
            OutlinedButton(
              onPressed: () => context.go('/dashboard/books'),
              child: const Text('Browse Books'),
            ),
          ],
        ),
      ),
    );
  }
}

/// A plain "you may already have this book" pointer shown above the format
/// panel when the library tracks what looks like the same title under another
/// catalog listing. Each candidate stays its own tappable row — records are
/// never merged — and opening one lands on the page whose request state is
/// real, which is the honest way to prevent an accidental duplicate request.
class _LookalikeNotice extends StatelessWidget {
  final List<OwnedTitle> candidates;
  final ValueChanged<OwnedTitle> onOpen;

  const _LookalikeNotice({required this.candidates, required this.onOpen});

  @override
  Widget build(BuildContext context) {
    return Material(
      color: AppTheme.surface,
      clipBehavior: Clip.antiAlias,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
        side: const BorderSide(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 12, 16, 10),
            child: Row(
              children: [
                Icon(Icons.library_books_outlined,
                    size: 18, color: AppTheme.requested),
                SizedBox(width: 8),
                Flexible(
                  child: Text(
                    'Your library may already have this book',
                    style: TextStyle(
                      color: AppTheme.textPrimary,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ],
            ),
          ),
          const Divider(height: 1, color: AppTheme.border),
          for (final candidate in candidates)
            ListTile(
              key: ValueKey(
                  'book-lookalike:${candidate.foreignBookId.trim()}'),
              dense: true,
              title: Text(
                candidate.title,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(color: AppTheme.textPrimary),
              ),
              subtitle: Text(
                _lookalikeStates(candidate.ownership),
                style: const TextStyle(
                  color: AppTheme.requested,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
              trailing: const Icon(Icons.chevron_right,
                  color: AppTheme.textSecondary),
              onTap: () => onOpen(candidate),
            ),
        ],
      ),
    );
  }
}

/// The same per-format state phrases the search results use, so the requester
/// reads one vocabulary everywhere.
String _lookalikeStates(BookOwnership o) => [
      if (o.ebook.downloaded)
        'eBook available'
      else if (o.ebook.monitored)
        'eBook requested',
      if (o.audiobook.downloaded)
        'Audiobook available'
      else if (o.audiobook.monitored)
        'Audiobook requested',
    ].join(' · ');

String _bookFileLabel(ChaptarrBookFile file, int index) {
  final path = file.path?.replaceAll('\\', '/') ?? '';
  final parts = path.split('/').where((part) => part.isNotEmpty).toList();
  return parts.isEmpty ? 'File ${index + 1}' : parts.last;
}

String _firstText(Iterable<String?> values) {
  for (final value in values) {
    final text = value?.trim() ?? '';
    if (text.isNotEmpty) return text;
  }
  return '';
}
