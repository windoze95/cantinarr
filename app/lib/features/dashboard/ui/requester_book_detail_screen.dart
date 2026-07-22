import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/status_pill.dart';
import '../../../navigation/ambient_page_route.dart';
import '../../auth/logic/auth_provider.dart';
import '../../chaptarr/data/chaptarr_api_service.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../../chaptarr/data/chaptarr_models.dart';
import '../../chaptarr/ui/chaptarr_book_screen.dart';
import '../../request/data/book_ownership.dart';
import '../../request/data/request_service.dart';
import '../../request/ui/book_request_button.dart';
import '../data/book_library_service.dart';

/// Requester-facing detail for one book, addressed by its Chaptarr/Readarr
/// foreignBookId. Search navigation supplies [initialBook] for an immediate,
/// metadata-rich presentation; notification/deep links resolve the same data
/// from the title hint when possible, and the owned-books digest remains the
/// live source of per-format ownership.
class RequesterBookDetailScreen extends ConsumerStatefulWidget {
  final String foreignId;
  final String? titleHint;
  final ChaptarrBook? initialBook;

  const RequesterBookDetailScreen({
    super.key,
    required this.foreignId,
    this.titleHint,
    this.initialBook,
  });

  @override
  ConsumerState<RequesterBookDetailScreen> createState() =>
      _RequesterBookDetailScreenState();
}

class _RequesterBookDetailScreenState
    extends ConsumerState<RequesterBookDetailScreen> {
  late final RequestService _requestService;
  ChaptarrBook? _metadata;
  List<ChaptarrBook> _chaptarrRecords = const [];
  BookRequestStatusDetail? _requestDetail;
  bool _metadataLoading = false;
  int _loadGeneration = 0;
  int _recordsLoadGeneration = 0;
  String? _instanceId;

  @override
  void initState() {
    super.initState();
    _requestService =
        RequestService(backendDio: ref.read(backendClientProvider));
    _startLoads();
  }

  @override
  void didUpdateWidget(covariant RequesterBookDetailScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.foreignId != widget.foreignId ||
        oldWidget.initialBook != widget.initialBook ||
        oldWidget.titleHint != widget.titleHint) {
      _startLoads();
    }
  }

  void _startLoads() {
    final generation = ++_loadGeneration;
    _recordsLoadGeneration++;
    _instanceId = ref.read(instanceProvider).activeChaptarrInstance?.id;
    _metadata = widget.initialBook;
    _chaptarrRecords = const [];
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
  /// metadata with the same read-only lookup as Books search, then require the
  /// exact foreign id so similarly named books cannot be substituted.
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
    } catch (_) {
      // The title hint still gives the requester a useful fallback.
    }
    if (!mounted || generation != _loadGeneration) return;
    setState(() {
      _metadata = match;
      _metadataLoading = false;
    });
  }

  /// Resolve an exact live Chaptarr destination for admins. Lookup records and
  /// the requester digest intentionally lack trustworthy numeric library ids,
  /// so only the live library list may back this link.
  Future<void> _resolveChaptarrRecords(int generation) async {
    final isAdmin = ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
    if (!isAdmin) return;
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
      if (!mounted ||
          generation != _loadGeneration ||
          recordsGeneration != _recordsLoadGeneration) {
        return;
      }
      setState(() => _chaptarrRecords = matches);
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
    ref.invalidate(ownedBooksProvider);
    ref.read(libraryRefreshTickProvider.notifier).state++;
    await _resolveChaptarrRecords(_loadGeneration);
  }

  void _openInChaptarr() {
    if (_chaptarrRecords.isEmpty) return;
    final instanceId = _instanceId;
    if (instanceId == null) return;
    Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => ChaptarrBookScreen(
          instanceId: instanceId,
          records: _chaptarrRecords,
          bookTitle: _chaptarrRecords.first.title,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final digest = ref.watch(ownedBooksProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('Book details')),
      body: digest.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppTheme.accent),
        ),
        // The digest normally degrades to an empty list on failure. Keep the
        // metadata/deep-link path usable if an override surfaces an error.
        error: (_, __) => _resolved(const []),
        data: _resolved,
      ),
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
        .withOwnership(ownership);

    final instanceId = _instanceId;
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
                service: _requestService,
                ownership: ownership,
                onDetailChanged: _onRequestDetailChanged,
                onRequestCompleted: _onRequestCompleted,
              ),
              if (_chaptarrRecords.isNotEmpty)
                OutlinedButton.icon(
                  onPressed: _openInChaptarr,
                  icon: const Icon(Icons.open_in_new_rounded, size: 17),
                  label: const Text('Open in Chaptarr'),
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

  const _FormatStatusPanel({
    required this.detail,
    required this.statusLoaded,
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

  const _FormatStatusRow({
    required this.icon,
    required this.label,
    required this.state,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      child: Row(
        children: [
          Icon(icon, color: AppTheme.accent, size: 20),
          const SizedBox(width: 12),
          Expanded(
            child: Text(label,
                style: Theme.of(context).textTheme.titleSmall),
          ),
          StatusPill(text: state.label, color: state.color),
        ],
      ),
    );
  }
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

  final status = detail.formats[format];
  if (status != null) {
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
      RequestStatus.unavailable =>
        (label: 'Not requested', color: AppTheme.textSecondary),
    };
  }
  if (!statusLoaded) {
    return (label: 'Checking…', color: AppTheme.textSecondary);
  }
  return (label: 'Not requested', color: AppTheme.textSecondary);
}

String _firstText(Iterable<String?> values) {
  for (final value in values) {
    final text = value?.trim() ?? '';
    if (text.isNotEmpty) return text;
  }
  return '';
}
