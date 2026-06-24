import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/status_pill.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';
import 'chaptarr_releases_screen.dart';
import 'widgets/book_status.dart';
import 'widgets/format_badge.dart';

/// Opens the book detail bottom sheet for one book. Returns `true` when the
/// caller should refresh (an Automatic/Interactive search was started from the
/// sheet). Mirrors [showModalBottomSheet] usage around the Sonarr episode sheet.
Future<bool?> showChaptarrBookDetailSheet(
  BuildContext context, {
  required String instanceId,
  required int bookId,
  String? bookTitle,
}) {
  return showModalBottomSheet<bool>(
    context: context,
    backgroundColor: Colors.transparent,
    isScrollControlled: true,
    builder: (_) => _ChaptarrBookDetailSheet(
      instanceId: instanceId,
      bookId: bookId,
      bookTitle: bookTitle,
    ),
  );
}

/// Bottom sheet for a single book: status, overview and recent history, with
/// Automatic/Interactive search actions. Mirrors [EpisodeDetailSheet], but
/// fetches the book + history itself (the caller passes only ids).
class _ChaptarrBookDetailSheet extends ConsumerStatefulWidget {
  final String instanceId;
  final int bookId;
  final String? bookTitle;

  const _ChaptarrBookDetailSheet({
    required this.instanceId,
    required this.bookId,
    this.bookTitle,
  });

  @override
  ConsumerState<_ChaptarrBookDetailSheet> createState() =>
      _ChaptarrBookDetailSheetState();
}

class _ChaptarrBookDetailSheetState
    extends ConsumerState<_ChaptarrBookDetailSheet> {
  late final ChaptarrApiService _service;
  ChaptarrBook? _book;
  List<ChaptarrHistoryRecord> _history = [];
  bool _loading = true;

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
    try {
      // Kick off both requests, then await — effectively parallel without the
      // heterogeneous Future.wait cast.
      final bookFuture = _service.getBookById(widget.bookId);
      final historyFuture = _service.getBookHistory(widget.bookId);
      final book = await bookFuture;
      final history = await historyFuture;
      if (!mounted) return;
      setState(() {
        _book = book;
        _history = history.take(8).toList();
        _loading = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loading = false);
    }
  }

  Future<void> _automaticSearch() async {
    try {
      await _service.searchBook([widget.bookId]);
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Book search started')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to start search: $e')));
    }
  }

  void _interactiveSearch() {
    Navigator.of(context, rootNavigator: true).push(
      MaterialPageRoute(
        builder: (_) => ChaptarrReleasesScreen(
          instanceId: widget.instanceId,
          bookId: widget.bookId,
          bookTitle: _book?.title ?? widget.bookTitle,
        ),
      ),
    );
  }

  ({String label, Color color}) get _shortStatus {
    final book = _book;
    if (book == null) {
      return (label: 'Loading…', color: AppTheme.textSecondary);
    }
    final line = bookFileStatusLine(book);
    return (label: line.text, color: line.color);
  }

  @override
  Widget build(BuildContext context) {
    final book = _book;
    final title = book?.title ?? widget.bookTitle ?? 'Book';
    final status = _shortStatus;
    final release = book?.releaseDate;
    final formats = book?.formats.toList() ?? [];
    formats.sort((a, b) => a.index - b.index);

    return Padding(
      padding:
          EdgeInsets.only(bottom: MediaQuery.of(context).viewInsets.bottom),
      child: Container(
        constraints: BoxConstraints(
            maxHeight: MediaQuery.of(context).size.height * 0.85),
        decoration: const BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        ),
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Center(
                child: Container(
                  width: 40,
                  height: 4,
                  decoration: BoxDecoration(
                    color: AppTheme.textSecondary,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              Text(
                title,
                style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 20,
                    fontWeight: FontWeight.bold),
              ),
              if (book?.author?.authorName.isNotEmpty ?? false) ...[
                const SizedBox(height: 4),
                Text(book!.author!.authorName,
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 14)),
              ],
              const SizedBox(height: 6),
              if (release != null)
                Text(DateFormat('MMMM d, yyyy').format(release),
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 13)),
              const SizedBox(height: 12),
              Wrap(
                spacing: 6,
                runSpacing: 4,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  StatusPill(text: status.label, color: status.color),
                  ...formats.map((f) => ChaptarrFormatBadge(format: f)),
                ],
              ),
              if (book?.overview != null && book!.overview!.isNotEmpty) ...[
                const SizedBox(height: 14),
                Text(book.overview!,
                    style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 14,
                        height: 1.4)),
              ],
              const SizedBox(height: 16),
              const Text('History',
                  style: TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 13,
                      fontWeight: FontWeight.w600)),
              const SizedBox(height: 8),
              _buildHistory(),
              const SizedBox(height: 16),
              Row(
                children: [
                  Expanded(
                    child: OutlinedButton.icon(
                      onPressed: () {
                        Navigator.of(context).pop(true);
                        _automaticSearch();
                      },
                      icon: const Icon(Icons.search,
                          size: 18, color: AppTheme.available),
                      label: const Text('Automatic',
                          style: TextStyle(color: AppTheme.textPrimary)),
                      style: OutlinedButton.styleFrom(
                        side: const BorderSide(color: AppTheme.border),
                        shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(10)),
                        padding: const EdgeInsets.symmetric(vertical: 12),
                      ),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: OutlinedButton.icon(
                      onPressed: () {
                        Navigator.of(context).pop(true);
                        _interactiveSearch();
                      },
                      icon: const Icon(Icons.manage_search,
                          size: 18, color: AppTheme.available),
                      label: const Text('Interactive',
                          style: TextStyle(color: AppTheme.textPrimary)),
                      style: OutlinedButton.styleFrom(
                        side: const BorderSide(color: AppTheme.border),
                        shape: RoundedRectangleBorder(
                            borderRadius: BorderRadius.circular(10)),
                        padding: const EdgeInsets.symmetric(vertical: 12),
                      ),
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHistory() {
    if (_loading) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 12),
        child: Center(
            child: SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(
                    strokeWidth: 2, color: AppTheme.accent))),
      );
    }
    if (_history.isEmpty) {
      return const Text('No history yet.',
          style: TextStyle(color: AppTheme.textSecondary, fontSize: 13));
    }
    return Column(
      children: _history.map((h) => _HistoryTile(record: h)).toList(),
    );
  }
}

String _historyEventLabel(ChaptarrHistoryRecord r) {
  switch (r.eventType) {
    case 'grabbed':
      final indexer = r.indexer;
      return indexer != null && indexer.isNotEmpty
          ? 'Grabbed from $indexer'
          : 'Grabbed';
    case 'bookFileImported':
    case 'downloadFolderImported':
      return 'Imported';
    case 'downloadFailed':
      return 'Download failed';
    case 'bookFileDeleted':
      return 'File deleted';
    case 'bookFileRenamed':
      return 'File renamed';
    case 'downloadIgnored':
      return 'Download ignored';
    default:
      return r.eventType.isEmpty ? 'Event' : r.eventType;
  }
}

String _historyTime(DateTime? date) {
  if (date == null) return '';
  final local = date.toLocal();
  final diff = DateTime.now().difference(local);
  final String rel;
  if (diff.inMinutes < 1) {
    rel = 'Just now';
  } else if (diff.inMinutes < 60) {
    rel = '${diff.inMinutes}m ago';
  } else if (diff.inHours < 24) {
    rel = '${diff.inHours}h ago';
  } else if (diff.inDays < 30) {
    rel = '${diff.inDays}d ago';
  } else {
    rel = DateFormat('MMM d, yyyy').format(local);
  }
  return '$rel • ${DateFormat('MMM d, yyyy • h:mm a').format(local)}';
}

class _HistoryTile extends StatelessWidget {
  final ChaptarrHistoryRecord record;
  const _HistoryTile({required this.record});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            record.sourceTitle.isNotEmpty
                ? record.sourceTitle
                : _historyEventLabel(record),
            style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                fontWeight: FontWeight.w500),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
          const SizedBox(height: 2),
          Text(_historyTime(record.date),
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 11)),
          const SizedBox(height: 2),
          Text(_historyEventLabel(record),
              style: const TextStyle(color: AppTheme.requested, fontSize: 12)),
        ],
      ),
    );
  }
}
