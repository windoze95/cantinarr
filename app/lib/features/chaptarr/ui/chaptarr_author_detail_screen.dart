import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';
import 'chaptarr_book_screen.dart';
import 'widgets/book_status.dart';
import 'widgets/format_badge.dart';

/// Author detail: an author summary plus the author's books, each with its
/// format badges, availability line and a monitor toggle. Tapping a book drills
/// into its editions/files. Mirrors [SonarrSeriesDetailScreen].
class ChaptarrAuthorDetailScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final int authorId;
  final String? authorName;

  const ChaptarrAuthorDetailScreen({
    super.key,
    required this.instanceId,
    required this.authorId,
    this.authorName,
  });

  @override
  ConsumerState<ChaptarrAuthorDetailScreen> createState() =>
      _ChaptarrAuthorDetailScreenState();
}

class _ChaptarrAuthorDetailScreenState
    extends ConsumerState<ChaptarrAuthorDetailScreen> {
  late final ChaptarrApiService _service;
  ChaptarrAuthor? _author;
  List<ChaptarrBook> _books = [];
  bool _isLoading = true;
  String? _error;
  final Set<int> _togglingBooks = {};

  @override
  void initState() {
    super.initState();
    _service = ChaptarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    setState(() => _isLoading = true);
    try {
      // Kick off both requests, then await — effectively parallel without the
      // heterogeneous Future.wait cast.
      final authorFuture = _service.getAuthorById(widget.authorId);
      final booksFuture = _service.getBooks(authorId: widget.authorId);
      final author = await authorFuture;
      final books = await booksFuture;
      if (!mounted) return;
      books.sort((a, b) => (b.releaseDate ?? DateTime(0))
          .compareTo(a.releaseDate ?? DateTime(0)));
      setState(() {
        _author = author;
        _books = books;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load author: $e';
      });
    }
  }

  Future<void> _toggleBookMonitored(ChaptarrBook book) async {
    final target = !book.monitored;
    setState(() => _togglingBooks.add(book.id));
    try {
      await _service.setBookMonitored([book.id], target);
      if (!mounted) return;
      // Reflect the change locally without a full reload.
      setState(() {
        _books = _books
            .map((b) => b.id == book.id ? _withMonitored(b, target) : b)
            .toList();
      });
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Could not change monitoring: $e')));
    } finally {
      if (mounted) setState(() => _togglingBooks.remove(book.id));
    }
  }

  void _openBook(ChaptarrBook book) {
    Navigator.of(context, rootNavigator: true).push(
      MaterialPageRoute(
        builder: (_) => ChaptarrBookScreen(
          instanceId: widget.instanceId,
          bookId: book.id,
          bookTitle: book.title,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final title = _author?.authorName ?? widget.authorName ?? 'Author';

    return Scaffold(
      backgroundColor: AppTheme.background,
      appBar: AppBar(
        backgroundColor: AppTheme.background,
        title: Text(title, maxLines: 1, overflow: TextOverflow.ellipsis),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh, color: AppTheme.textPrimary),
            tooltip: 'Refresh',
            onPressed: _isLoading ? null : _load,
          ),
        ],
      ),
      body: _error != null && _author == null
          ? FullScreenError(message: _error!, onRetry: _load)
          : RefreshIndicator(
              onRefresh: _load,
              color: AppTheme.accent,
              child: ListView(
                padding: const EdgeInsets.symmetric(vertical: 12),
                children: [
                  if (_error != null)
                    ErrorBanner(message: _error!, onRetry: _load),
                  if (_author != null) _AuthorSummaryCard(author: _author!),
                  const SizedBox(height: 4),
                  ..._books.map((b) => _BookCard(
                        book: b,
                        busy: _togglingBooks.contains(b.id),
                        onTap: () => _openBook(b),
                        onToggleMonitored: () => _toggleBookMonitored(b),
                      )),
                  if (_books.isEmpty && !_isLoading)
                    const Padding(
                      padding: EdgeInsets.all(32),
                      child: Center(
                        child: Text('No books',
                            style: TextStyle(color: AppTheme.textSecondary)),
                      ),
                    ),
                ],
              ),
            ),
    );
  }
}

/// Rebuilds a [ChaptarrBook] with a new monitored flag (the model is immutable
/// and exposes no copyWith).
ChaptarrBook _withMonitored(ChaptarrBook b, bool monitored) => ChaptarrBook(
      id: b.id,
      title: b.title,
      authorId: b.authorId,
      foreignBookId: b.foreignBookId,
      titleSlug: b.titleSlug,
      overview: b.overview,
      releaseDate: b.releaseDate,
      monitored: monitored,
      anyEditionOk: b.anyEditionOk,
      pageCount: b.pageCount,
      author: b.author,
      statistics: b.statistics,
      editions: b.editions,
      images: b.images,
      genres: b.genres,
    );

/// "X/Y Books Available" with a colour: green at 100%, red otherwise.
class _AvailabilityLine extends StatelessWidget {
  final ChaptarrAuthorStatistics? stats;
  const _AvailabilityLine({required this.stats});

  @override
  Widget build(BuildContext context) {
    final s = stats;
    if (s == null || s.bookCount == 0) {
      return const Text('0% • 0/0 Books Available',
          style: TextStyle(color: AppTheme.textSecondary, fontSize: 13));
    }
    final pct = (s.bookFileCount / s.bookCount * 100).round();
    final complete = s.bookFileCount >= s.bookCount;
    return Text(
      '$pct% • ${s.bookFileCount}/${s.bookCount} Books Available',
      style: TextStyle(
        color: complete ? AppTheme.available : AppTheme.error,
        fontSize: 13,
        fontWeight: FontWeight.w500,
      ),
    );
  }
}

class _AuthorSummaryCard extends StatelessWidget {
  final ChaptarrAuthor author;
  const _AuthorSummaryCard({required this.author});

  @override
  Widget build(BuildContext context) {
    final stats = author.statistics;
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border, width: 0.5),
      ),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(author.authorName,
                    style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 17,
                        fontWeight: FontWeight.bold)),
                if (stats != null && stats.sizeOnDisk > 0) ...[
                  const SizedBox(height: 4),
                  Text(stats.sizeFormatted,
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 13)),
                ],
                const SizedBox(height: 6),
                _AvailabilityLine(stats: stats),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _BookCard extends StatelessWidget {
  final ChaptarrBook book;
  final bool busy;
  final VoidCallback onTap;
  final VoidCallback onToggleMonitored;

  const _BookCard({
    required this.book,
    required this.busy,
    required this.onTap,
    required this.onToggleMonitored,
  });

  @override
  Widget build(BuildContext context) {
    final status = bookFileStatusLine(book);
    final formats = book.formats.toList()..sort((a, b) => a.index - b.index);
    return InkWell(
      onTap: onTap,
      child: Container(
        margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(color: AppTheme.border, width: 0.5),
        ),
        child: Row(
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(6),
              child: SizedBox(
                width: 44,
                height: 60,
                child: book.coverUrl != null
                    ? CachedNetworkImage(
                        imageUrl: book.coverUrl!, fit: BoxFit.cover)
                    : Container(
                        color: AppTheme.surfaceVariant,
                        child: Icon(
                          Icons.menu_book_outlined,
                          color: book.monitored
                              ? AppTheme.available
                              : AppTheme.unavailable,
                          size: 22,
                        ),
                      ),
              ),
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(book.title,
                      style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 16,
                          fontWeight: FontWeight.w600),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis),
                  if (formats.isNotEmpty) ...[
                    const SizedBox(height: 6),
                    Wrap(
                      spacing: 6,
                      runSpacing: 4,
                      children: formats
                          .map((f) => ChaptarrFormatBadge(format: f))
                          .toList(),
                    ),
                  ],
                  const SizedBox(height: 6),
                  Text(
                    status.text,
                    style: TextStyle(
                        color: status.color,
                        fontSize: 13,
                        fontWeight: FontWeight.w500),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            IconButton(
              onPressed: busy ? null : onToggleMonitored,
              tooltip: book.monitored ? 'Stop monitoring' : 'Monitor',
              icon: busy
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(
                          strokeWidth: 2, color: AppTheme.accent))
                  : Icon(
                      book.monitored ? Icons.bookmark : Icons.bookmark_border,
                      color: book.monitored
                          ? AppTheme.accent
                          : AppTheme.textSecondary,
                    ),
            ),
          ],
        ),
      ),
    );
  }
}
