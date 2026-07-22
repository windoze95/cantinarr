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
import '../../../core/widgets/status_pill.dart';
import '../../../navigation/ambient_page_route.dart';
import '../../auth/logic/auth_provider.dart';
import '../../chaptarr/data/chaptarr_api_service.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../../chaptarr/data/chaptarr_models.dart';
import '../../chaptarr/ui/chaptarr_book_screen.dart';
import '../../media_download/data/media_download_models.dart';
import '../../media_download/ui/media_download_button.dart';
import '../../request/data/book_ownership.dart';
import '../../request/data/request_service.dart';
import '../../request/ui/book_request_button.dart';
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

  const RequesterBookDetailScreen({
    super.key,
    required this.foreignId,
    this.titleHint,
    this.initialBook,
    this.instanceId,
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
  BookRequestStatusDetail? _requestDetail;
  bool _metadataLoading = false;
  int _loadGeneration = 0;
  int _recordsLoadGeneration = 0;
  String? _instanceId;

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
    _requestDetail = null;
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
        auth?.connection?.services.mediaDownloads ?? false;
    if (!isAdmin && !downloadsEnabled) return;
    final service = _chaptarrService();
    if (service == null) return;
    final recordsGeneration = ++_recordsLoadGeneration;
    try {
      final books = await service.getBooks();
      final matches = books
          .where((book) =>
              book.id > 0 && book.foreignBookId == widget.foreignId)
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

  void _onRequestDetailChanged(BookRequestStatusDetail detail) {
    if (!mounted) return;
    setState(() => _requestDetail = detail);
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

  Widget _resolved(List<OwnedTitle> titles) {
    OwnedTitle? owned;
    for (final title in titles) {
      if (title.foreignBookId.isNotEmpty &&
          title.foreignBookId == widget.foreignId) {
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
    final detail = (_requestDetail ?? const BookRequestStatusDetail())
        .withOwnership(
          ownership,
          ownershipStatusKnown: owned?.statusKnown ?? true,
        );
    final auth = ref.watch(authProvider).valueOrNull;
    final downloadsEnabled = auth?.connection?.services.mediaDownloads ?? false;
    final ebookFiles = _downloadChoicesFor(BookFormat.ebook);
    final audiobookFiles = _downloadChoicesFor(BookFormat.audiobook);
    final isAdmin = auth?.user?.isAdmin ?? false;

    final instanceId = _instanceId;
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
        // Build the status/action region even when large accessibility text
        // pushes it just below the viewport; it owns the live status refresh
        // that feeds the format panel above it.
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
          _FormatStatusPanel(
            detail: detail,
            statusLoaded: _requestDetail != null,
            instanceId: instanceId,
            ebookFiles: downloadsEnabled ? ebookFiles : const [],
            audiobookFiles:
                downloadsEnabled ? audiobookFiles : const [],
          ),
          const SizedBox(height: 18),
          Wrap(
            alignment: WrapAlignment.center,
            crossAxisAlignment: WrapCrossAlignment.center,
            spacing: 10,
            runSpacing: 8,
            children: [
              BookRequestButton(
                foreignId: widget.foreignId,
                title: title,
                instanceId: instanceId,
                service: _requestService,
                ownership: ownership,
                ownershipStatusKnown: owned?.statusKnown ?? true,
                refreshTick: requestRefreshTick,
                showCoveredStatus: false,
                onDetailChanged: _onRequestDetailChanged,
                onRequestCompleted: _onRequestCompleted,
              ),
              if (isAdmin && _chaptarrRecords.isNotEmpty)
                OutlinedButton.icon(
                  onPressed: _openInChaptarr,
                  icon: const Icon(Icons.open_in_new_rounded, size: 17),
                  label: const Text('Manage book'),
                ),
            ],
          ),
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

class _FormatStatusPanel extends StatelessWidget {
  final BookRequestStatusDetail detail;
  final bool statusLoaded;
  final String? instanceId;
  final List<MediaDownloadChoice> ebookFiles;
  final List<MediaDownloadChoice> audiobookFiles;

  const _FormatStatusPanel({
    required this.detail,
    required this.statusLoaded,
    required this.instanceId,
    required this.ebookFiles,
    required this.audiobookFiles,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 14, 16, 10),
            child: Text('Formats',
                style: Theme.of(context).textTheme.titleSmall),
          ),
          const Divider(height: 1, color: AppTheme.border),
          _FormatStatusRow(
            icon: Icons.menu_book,
            label: 'eBook',
            state: _formatState(
              detail,
              BookRequestFormat.ebook,
              statusLoaded,
            ),
            download: instanceId == null || ebookFiles.isEmpty
                ? null
                : MediaDownloadChoiceButton(
                    instanceId: instanceId!,
                    choices: ebookFiles,
                    label: 'Download eBook',
                    sheetTitle: 'Download eBook',
                    iconOnly: true,
                  ),
          ),
          const Divider(height: 1, indent: 52, color: AppTheme.border),
          _FormatStatusRow(
            icon: Icons.headphones,
            label: 'Audiobook',
            state: _formatState(
              detail,
              BookRequestFormat.audiobook,
              statusLoaded,
            ),
            download: instanceId == null || audiobookFiles.isEmpty
                ? null
                : MediaDownloadChoiceButton(
                    instanceId: instanceId!,
                    choices: audiobookFiles,
                    label: 'Download audiobook',
                    sheetTitle: 'Download audiobook',
                    iconOnly: true,
                  ),
          ),
        ],
      ),
    );
  }
}

class _FormatStatusRow extends StatelessWidget {
  final IconData icon;
  final String label;
  final ({String label, Color color}) state;
  final Widget? download;

  const _FormatStatusRow({
    required this.icon,
    required this.label,
    required this.state,
    this.download,
  });

  @override
  Widget build(BuildContext context) {
    final stack = MediaQuery.textScalerOf(context).scale(1) > 1.3 ||
        MediaQuery.sizeOf(context).width < 360 ||
        state.label == 'Format needs attention';
    final heading = Row(
      children: [
        Icon(icon, color: AppTheme.accent, size: 20),
        const SizedBox(width: 12),
        Expanded(
          child: Text(label, style: Theme.of(context).textTheme.titleSmall),
        ),
      ],
    );
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      child: stack
          ? Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                heading,
                const SizedBox(height: 8),
                Padding(
                  padding: const EdgeInsets.only(left: 28),
                  child: Row(
                    children: [
                      Expanded(
                        child: Align(
                          alignment: Alignment.centerLeft,
                          child: StatusPill(
                            text: state.label,
                            color: state.color,
                          ),
                        ),
                      ),
                      if (download != null) ...[
                        const SizedBox(width: 4),
                        download!,
                      ],
                    ],
                  ),
                ),
              ],
            )
          : Row(
              children: [
                Expanded(child: heading),
                StatusPill(text: state.label, color: state.color),
                if (download != null) ...[
                  const SizedBox(width: 4),
                  download!,
                ],
              ],
            ),
    );
  }
}

String _bookFileLabel(ChaptarrBookFile file, int index) {
  final path = file.path?.replaceAll('\\', '/') ?? '';
  final parts = path.split('/').where((part) => part.isNotEmpty).toList();
  return parts.isEmpty ? 'File ${index + 1}' : parts.last;
}

({String label, Color color}) _formatState(
  BookRequestStatusDetail detail,
  BookRequestFormat format,
  bool statusLoaded,
) {
  final ownership = detail.ownership;
  final owned = switch (format) {
    BookRequestFormat.ebook => ownership?.ebook,
    BookRequestFormat.audiobook => ownership?.audiobook,
    BookRequestFormat.both => null,
  };
  if (owned?.downloaded ?? false) {
    return (label: 'Available', color: AppTheme.available);
  }

  if (owned?.monitored ?? false) {
    return (label: 'Requested', color: AppTheme.requested);
  }

  final status = detail.statusFor(format);
  if (status != null && status != RequestStatus.unavailable) {
    return switch (status) {
      RequestStatus.available =>
        (label: 'Available', color: AppTheme.available),
      RequestStatus.pending =>
        (label: 'Pending Approval', color: AppTheme.requested),
      RequestStatus.requested =>
        (label: 'Requested', color: AppTheme.requested),
      RequestStatus.downloading =>
        (label: 'Downloading', color: AppTheme.downloading),
      RequestStatus.partial =>
        (label: 'Requested', color: AppTheme.requested),
      RequestStatus.denied =>
        (label: 'Request Denied', color: AppTheme.error),
      RequestStatus.unavailable => throw StateError('unreachable'),
    };
  }

  // Until request history resolves, an empty ownership row does not prove the
  // format was never requested. Keep the neutral loading state instead of
  // briefly claiming "Not requested" for a pending or denied request.
  if (!statusLoaded) {
    return (label: 'Checking…', color: AppTheme.textSecondary);
  }

  if (!detail.isKnown) {
    return detail.effectiveUnknownReason ==
            BookStatusUnknownReason.formatNeedsAttention
        ? (label: 'Format needs attention', color: AppTheme.requested)
        : (label: 'Couldn’t check', color: AppTheme.error);
  }
  if (status == RequestStatus.unavailable) {
    return (label: 'Not requested', color: AppTheme.textSecondary);
  }
  return (label: 'Couldn’t check', color: AppTheme.error);
}

String _firstText(Iterable<String?> values) {
  for (final value in values) {
    final text = value?.trim() ?? '';
    if (text.isNotEmpty) return text;
  }
  return '';
}
