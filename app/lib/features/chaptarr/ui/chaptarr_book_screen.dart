import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';
import 'chaptarr_book_detail_sheet.dart';
import 'chaptarr_releases_screen.dart';
import 'widgets/book_status.dart';
import 'widgets/format_badge.dart';
import 'widgets/format_picker.dart';
import 'widgets/search_actions.dart';

/// One title's formats. Chaptarr stores a title's ebook and audiobook as
/// separate book records (same foreignBookId); [records] holds the 1–2 records.
/// Shows a section per format with its availability + matched file, and an
/// action bar whose Automatic/Interactive searches prompt for the format when
/// the title has both. Mirrors [SonarrSeasonScreen].
class ChaptarrBookScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final List<ChaptarrBook> records;
  final String? bookTitle;

  const ChaptarrBookScreen({
    super.key,
    required this.instanceId,
    required this.records,
    this.bookTitle,
  });

  @override
  ConsumerState<ChaptarrBookScreen> createState() => _ChaptarrBookScreenState();
}

class _ChaptarrBookScreenState extends ConsumerState<ChaptarrBookScreen> {
  late final ChaptarrApiService _service;
  late List<ChaptarrBook> _liveRecords;
  // Downloaded files keyed by record (book) id.
  Map<int, List<ChaptarrBookFile>> _filesByBook = {};
  bool _isLoading = true;
  String? _error;

  List<ChaptarrBook> get _records => _liveRecords;

  @override
  void initState() {
    super.initState();
    _service = ChaptarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    _liveRecords = List<ChaptarrBook>.from(widget.records);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    setState(() => _isLoading = true);
    try {
      final seed = _records.first;
      final library = seed.authorId > 0
          ? await _service.getBooks(authorId: seed.authorId)
          : await Future.wait(_records.map((r) => _service.getBookById(r.id)));
      final refreshed = library
          .where((book) => book.groupKey == seed.groupKey)
          .toList()
        ..sort((a, b) => a.format.index.compareTo(b.format.index));
      final records = refreshed.isEmpty ? _records : refreshed;
      final results = await Future.wait(
        records.map((r) => _service.getBookFiles(bookId: r.id)),
      );
      if (!mounted) return;
      final map = <int, List<ChaptarrBookFile>>{};
      for (var i = 0; i < records.length; i++) {
        map[records[i].id] = results[i];
      }
      setState(() {
        _liveRecords = records;
        _filesByBook = map;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load book: $e';
      });
    }
  }

  Future<void> _automaticSearch() async {
    final chosen = await pickFormatRecords(context, _records);
    if (chosen == null || !mounted) return;
    final primary = chosen.first;
    try {
      await _service.searchBook(chosen.map((record) => record.id).toList());
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text(
              'Searching for ${chaptarrFormatLabel(primary.format)} — ${primary.title}…')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Search failed: $e')));
    }
  }

  Future<void> _interactiveSearch() async {
    final chosen = await pickInteractiveFormatRecord(context, _records);
    if (chosen == null || !mounted) return;
    Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => ChaptarrReleasesScreen(
          instanceId: widget.instanceId,
          bookId: chosen.id,
          bookTitle: chosen.title,
        ),
      ),
    );
  }

  Future<void> _openDetail() async {
    final changed = await showChaptarrBookDetailSheet(
      context,
      instanceId: widget.instanceId,
      records: _records,
      bookTitle: widget.bookTitle,
    );
    // The sheet returns true after an Automatic/Interactive search.
    if (changed == true && mounted) _load();
  }

  @override
  Widget build(BuildContext context) {
    final primary = _records.first;
    final title =
        primary.title.isNotEmpty ? primary.title : (widget.bookTitle ?? 'Book');
    final author = primary.author?.authorName;

    return Scaffold(
      backgroundColor: AppTheme.background,
      appBar: AppBar(
        backgroundColor: AppTheme.background,
        title: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(title, maxLines: 1, overflow: TextOverflow.ellipsis),
            if (author != null && author.isNotEmpty)
              Text(
                author,
                style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 12,
                    fontWeight: FontWeight.w400),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
          ],
        ),
      ),
      body: CenteredContent(
          child: Column(
        children: [
          Expanded(child: _buildBody()),
          _ActionBar(
            onAutomatic: _automaticSearch,
            onInteractive: _interactiveSearch,
          ),
        ],
      )),
    );
  }

  Widget _buildBody() {
    if (_error != null) {
      return FullScreenError(message: _error!, onRetry: _load);
    }
    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: ListView(
        padding: const EdgeInsets.symmetric(vertical: 8),
        children: [
          _BookHeader(records: _records, onTap: _openDetail),
          const Divider(color: AppTheme.border, height: 1),
          for (final r in _records)
            _FormatSection(
              record: r,
              files: _filesByBook[r.id] ?? const [],
              loading: _isLoading,
              onTap: _openDetail,
            ),
        ],
      ),
    );
  }
}

/// The book summary header — title, release date, availability line. Tapping it
/// opens the detail sheet.
class _BookHeader extends StatelessWidget {
  final List<ChaptarrBook> records;
  final VoidCallback onTap;

  const _BookHeader({required this.records, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final book = records.first;
    final status = groupedBookStatusLine(records);
    final release = book.releaseDate;
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(book.title,
                      style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 16,
                          fontWeight: FontWeight.w600)),
                  if (release != null) ...[
                    const SizedBox(height: 2),
                    Text(DateFormat('MMMM d, yyyy').format(release),
                        style: const TextStyle(
                            color: AppTheme.textSecondary, fontSize: 12)),
                  ],
                  const SizedBox(height: 4),
                  Text(
                    status.text,
                    style: TextStyle(
                        color: status.color,
                        fontSize: 13,
                        fontWeight: FontWeight.w500),
                  ),
                ],
              ),
            ),
            const Icon(Icons.chevron_right, color: AppTheme.textSecondary),
          ],
        ),
      ),
    );
  }
}

/// One format (ebook or audiobook) of a title: a format badge, this record's
/// availability/file line, and a read-only monitor indicator (the toggle lives
/// on the author screen's card).
class _FormatSection extends StatelessWidget {
  final ChaptarrBook record;
  final List<ChaptarrBookFile> files;
  final bool loading;
  final VoidCallback onTap;

  const _FormatSection({
    required this.record,
    required this.files,
    required this.loading,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final status = bookFileStatusLine(record);
    final file = files.isNotEmpty ? files.first : null;
    final hasFile = file != null;
    final line = hasFile
        ? '${file.qualityName ?? 'Downloaded'} — ${file.sizeFormatted}'
        : (loading ? 'Checking…' : status.text);
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      ChaptarrFormatBadge(format: record.format),
                      if (record.format == BookFormat.unknown)
                        const Text('Book',
                            style: TextStyle(
                                color: AppTheme.textPrimary,
                                fontSize: 14,
                                fontWeight: FontWeight.w500)),
                    ],
                  ),
                  const SizedBox(height: 4),
                  Text(
                    line,
                    style: TextStyle(
                        color: hasFile ? AppTheme.available : status.color,
                        fontSize: 13,
                        fontWeight: FontWeight.w500),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            Tooltip(
              message: record.monitored
                  ? 'Tracked ${chaptarrFormatLabel(record.format)}'
                  : 'Not tracked ${chaptarrFormatLabel(record.format)}',
              child: SizedBox(
                width: 48,
                height: 48,
                child: Icon(
                  record.monitored ? Icons.bookmark : Icons.bookmark_border,
                  size: 20,
                  color: record.monitored
                      ? AppTheme.accent
                      : AppTheme.textSecondary,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ActionBar extends StatelessWidget {
  final VoidCallback onAutomatic;
  final VoidCallback onInteractive;

  const _ActionBar({required this.onAutomatic, required this.onInteractive});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: EdgeInsets.fromLTRB(
          12, 10, 12, 10 + MediaQuery.of(context).padding.bottom),
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        border: Border(top: BorderSide(color: AppTheme.border, width: 0.5)),
      ),
      child: ChaptarrSearchActions(
        onFindAutomatically: onAutomatic,
        onChooseDownload: onInteractive,
      ),
    );
  }
}
