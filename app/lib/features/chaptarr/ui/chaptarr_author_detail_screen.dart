import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/section_header.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/chaptarr_add_payload.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_image.dart';
import '../data/chaptarr_models.dart';
import 'chaptarr_book_screen.dart';
import 'widgets/book_status.dart';
import 'widgets/format_picker.dart';

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
  // Keys ("<groupKey>:<format index>") of format records currently being added.
  final Set<String> _addingFormats = {};

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
      var movedSections = false;
      setState(() {
        final wasMonitored = _books.any((candidate) =>
            candidate.groupKey == book.groupKey && candidate.monitored);
        _books = _books
            .map((b) => b.id == book.id ? _withMonitored(b, target) : b)
            .toList();
        final isMonitored = _books.any((candidate) =>
            candidate.groupKey == book.groupKey && candidate.monitored);
        movedSections = wasMonitored != isMonitored;
      });
      final format = chaptarrFormatLabel(book.format);
      final message = movedSections
          ? target
              ? '${book.title} moved to Monitored books'
              : '${book.title} moved out of Monitored books'
          : target
              ? 'Monitoring $format for ${book.title}'
              : 'Stopped monitoring $format for ${book.title}';
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(message)));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Could not change monitoring: $e')));
    } finally {
      if (mounted) setState(() => _togglingBooks.remove(book.id));
    }
  }

  void _openBookGroup(List<ChaptarrBook> records) {
    Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => ChaptarrBookScreen(
          instanceId: widget.instanceId,
          records: records,
          bookTitle: records.first.title,
        ),
      ),
    );
  }

  /// Groups the flat book list into one entry per title: Chaptarr stores a
  /// title's ebook and audiobook as separate records sharing a foreignBookId.
  /// Insertion order (already release-date sorted) is preserved; within a group
  /// ebook is ordered before audiobook.
  List<List<ChaptarrBook>> _groupedBooks() {
    final groups = <String, List<ChaptarrBook>>{};
    for (final b in _books) {
      (groups[b.groupKey] ??= []).add(b);
    }
    for (final records in groups.values) {
      records.sort((a, b) => a.format.index.compareTo(b.format.index));
    }
    return groups.values.toList();
  }

  Widget _bookCard(List<ChaptarrBook> records) => _BookCard(
        key: ValueKey('book:${records.first.groupKey}'),
        records: records,
        cover: chaptarrImageSource(
            ref, records.first.coverUrl, widget.instanceId),
        togglingIds: _togglingBooks,
        addingKeys: _addingFormats,
        onTap: () => _openBookGroup(records),
        onToggleRecord: _toggleBookMonitored,
        onAddFormat: (format) => _addFormat(records, format),
      );

  String _addKey(List<ChaptarrBook> records, BookFormat format) =>
      '${records.first.groupKey}:${format.index}';

  /// Starts monitoring a format the title doesn't have a record for yet, by
  /// creating a monitored Chaptarr record sharing the title's foreignBookId.
  /// Format is never pre-determined — this is just the unmonitored bookmark for
  /// that format being switched on, the same as monitoring any record. The
  /// author already exists here, so the record stays monitored (no editions
  /// needed) and searchForNewBook starts a download search.
  Future<void> _addFormat(List<ChaptarrBook> records, BookFormat format) async {
    final primary = records.first;
    final foreignBookId = primary.foreignBookId;
    if (foreignBookId == null || foreignBookId.isEmpty) return;
    final label = chaptarrFormatLabel(format);
    final key = _addKey(records, format);
    setState(() => _addingFormats.add(key));
    try {
      final qps = await _service.getQualityProfiles();
      final mps = await _service.getMetadataProfiles();
      final folders = await _service.getRootFolders();
      if (qps.isEmpty || mps.isEmpty || folders.isEmpty) {
        throw Exception('a quality/metadata profile or root folder is missing');
      }
      await _service.addBook(chaptarrAddFormatBody(
        foreignBookId: foreignBookId,
        title: primary.title,
        titleSlug: primary.titleSlug,
        format: format,
        authorName: _author?.authorName ?? primary.author?.authorName ?? '',
        foreignAuthorId:
            _author?.foreignAuthorId ?? primary.author?.foreignAuthorId,
        qualityProfileId: qps.first.id,
        metadataProfileId: mps.first.id,
        rootFolderPath: chaptarrRootFolderFor(format, folders),
      ));
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content:
              Text('Monitoring $label — searching for ${primary.title}…')));
      await _load();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Could not monitor $label: $e')));
    } finally {
      if (mounted) setState(() => _addingFormats.remove(key));
    }
  }

  @override
  Widget build(BuildContext context) {
    final title = _author?.authorName ?? widget.authorName ?? 'Author';
    final groupedBooks = _groupedBooks();
    final monitoredBooks = <List<ChaptarrBook>>[];
    final otherBooks = <List<ChaptarrBook>>[];
    // A title belongs up top when either of its format records is monitored.
    // Append into each bucket so the existing newest-first order stays stable.
    for (final records in groupedBooks) {
      if (records.any((book) => book.monitored)) {
        monitoredBooks.add(records);
      } else {
        otherBooks.add(records);
      }
    }

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
      body: CenteredContent(
          child: _error != null && _author == null
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
                      if (monitoredBooks.isNotEmpty) ...[
                        const _BookSectionHeading(title: 'Monitored books'),
                        ...monitoredBooks.map(_bookCard),
                      ],
                      if (otherBooks.isNotEmpty) ...[
                        if (monitoredBooks.isNotEmpty)
                          const _BookSectionHeading(title: 'Other books'),
                        ...otherBooks.map(_bookCard),
                      ],
                      if (_books.isEmpty && !_isLoading)
                        const Padding(
                          padding: EdgeInsets.all(32),
                          child: Center(
                            child: Text('No books',
                                style:
                                    TextStyle(color: AppTheme.textSecondary)),
                          ),
                        ),
                    ],
                  ),
                )),
    );
  }
}

class _BookSectionHeading extends StatelessWidget {
  final String title;

  const _BookSectionHeading({required this.title});

  @override
  Widget build(BuildContext context) {
    return Semantics(
      header: true,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
        child: SectionHeader(title: title),
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
      mediaType: b.mediaType,
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

/// One card per title. Chaptarr stores a title's ebook and audiobook as
/// separate records (same foreignBookId); [records] holds the 1–2 records so the
/// card shows a single entry with, per format, a monitor toggle (when that
/// format exists) or an add button (when it doesn't).
class _BookCard extends StatelessWidget {
  final List<ChaptarrBook> records;
  final ChaptarrImageSource? cover;
  final Set<int> togglingIds;
  final Set<String> addingKeys;
  final VoidCallback onTap;
  final void Function(ChaptarrBook record) onToggleRecord;
  final void Function(BookFormat format) onAddFormat;

  const _BookCard({
    super.key,
    required this.records,
    required this.cover,
    required this.togglingIds,
    required this.addingKeys,
    required this.onTap,
    required this.onToggleRecord,
    required this.onAddFormat,
  });

  @override
  Widget build(BuildContext context) {
    final primary = records.first;
    // Prefer a downloaded record for the status line, else the first record.
    final fileRecord =
        records.firstWhere((r) => r.hasFile, orElse: () => primary);
    final status = bookFileStatusLine(fileRecord);
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
                child: CachedImage(
                  url: cover?.url,
                  headers: cover?.headers,
                  fit: BoxFit.cover,
                  icon: Icons.menu_book_outlined,
                  iconSize: 22,
                ),
              ),
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(primary.title,
                      style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 16,
                          fontWeight: FontWeight.w600),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis),
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
            const SizedBox(width: 8),
            Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                _formatControl(BookFormat.ebook),
                _formatControl(BookFormat.audiobook),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Widget _formatControl(BookFormat format) {
    final record = _recordForFormat(records, format);
    final busy = (record != null && togglingIds.contains(record.id)) ||
        addingKeys.contains('${records.first.groupKey}:${format.index}');
    return _FormatControl(
      format: format,
      record: record,
      busy: busy,
      onToggle: onToggleRecord,
      onAdd: () => onAddFormat(format),
    );
  }
}

/// Finds a title's record for a format, or null when nothing is monitored as
/// that format yet.
ChaptarrBook? _recordForFormat(List<ChaptarrBook> records, BookFormat format) {
  for (final r in records) {
    if (r.format == format) return r;
  }
  return null;
}

/// A per-format monitoring bookmark on the right of a book card. Format is never
/// pre-determined: every title shows both an ebook and an audiobook bookmark,
/// filled when that format is monitored and empty otherwise. Tapping an empty
/// bookmark monitors that format — creating the record if the title doesn't have
/// one yet — and tapping a filled one stops monitoring it.
class _FormatControl extends StatelessWidget {
  final BookFormat format;
  final ChaptarrBook? record;
  final bool busy;
  final void Function(ChaptarrBook record) onToggle;
  final VoidCallback onAdd;

  const _FormatControl({
    required this.format,
    required this.record,
    required this.busy,
    required this.onToggle,
    required this.onAdd,
  });

  @override
  Widget build(BuildContext context) {
    final label = chaptarrFormatLabel(format);
    final r = record;
    final monitored = r?.monitored ?? false;
    return Tooltip(
      message: monitored ? 'Stop monitoring $label' : 'Monitor $label',
      // Tap an existing record to toggle its monitoring; tap an empty bookmark
      // with no record to start monitoring that format (which creates it).
      child: InkWell(
        onTap: busy ? null : (r != null ? () => onToggle(r) : onAdd),
        borderRadius: BorderRadius.circular(8),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 6),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(chaptarrFormatIcon(format),
                  size: 14, color: AppTheme.textSecondary),
              const SizedBox(width: 4),
              busy
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(
                          strokeWidth: 2, color: AppTheme.accent),
                    )
                  : Icon(
                      monitored ? Icons.bookmark : Icons.bookmark_border,
                      size: 20,
                      color:
                          monitored ? AppTheme.accent : AppTheme.textSecondary,
                    ),
            ],
          ),
        ),
      ),
    );
  }
}
