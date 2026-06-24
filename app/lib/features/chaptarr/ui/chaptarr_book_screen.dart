import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/chaptarr_api_service.dart';
import '../data/chaptarr_models.dart';
import 'chaptarr_book_detail_sheet.dart';
import 'chaptarr_releases_screen.dart';
import 'widgets/book_status.dart';
import 'widgets/format_badge.dart';

/// Editions/files for one book: each edition's title, format badge and the
/// matched file's quality+size (or a Missing/Unreleased line). Tapping the book
/// header opens its detail sheet; the action bar runs a per-book search.
/// Mirrors [SonarrSeasonScreen].
class ChaptarrBookScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final int bookId;
  final String? bookTitle;

  const ChaptarrBookScreen({
    super.key,
    required this.instanceId,
    required this.bookId,
    this.bookTitle,
  });

  @override
  ConsumerState<ChaptarrBookScreen> createState() => _ChaptarrBookScreenState();
}

class _ChaptarrBookScreenState extends ConsumerState<ChaptarrBookScreen> {
  late final ChaptarrApiService _service;
  ChaptarrBook? _book;
  List<ChaptarrBookFile> _files = [];
  bool _isLoading = true;
  String? _error;

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
      final bookFuture = _service.getBookById(widget.bookId);
      final filesFuture = _service.getBookFiles(bookId: widget.bookId);
      final book = await bookFuture;
      final files = await filesFuture;
      if (!mounted) return;
      setState(() {
        _book = book;
        _files = files;
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
    try {
      await _service.searchBook([widget.bookId]);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Searching for ${_book?.title ?? 'book'}…')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Search failed: $e')));
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

  Future<void> _openDetail() async {
    final changed = await showChaptarrBookDetailSheet(
      context,
      instanceId: widget.instanceId,
      bookId: widget.bookId,
      bookTitle: _book?.title ?? widget.bookTitle,
    );
    // The book sheet returns true after an Automatic/Interactive search.
    if (changed == true && mounted) _load();
  }

  @override
  Widget build(BuildContext context) {
    final title = _book?.title ?? widget.bookTitle ?? 'Book';
    final author = _book?.author?.authorName;

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
      body: Column(
        children: [
          Expanded(child: _buildBody()),
          _ActionBar(
            onAutomatic: _automaticSearch,
            onInteractive: _interactiveSearch,
          ),
        ],
      ),
    );
  }

  Widget _buildBody() {
    if (_isLoading && _book == null) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null && _book == null) {
      return FullScreenError(message: _error!, onRetry: _load);
    }
    final book = _book;
    if (book == null) {
      return const Center(
        child: Text('No book',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 16)),
      );
    }
    final fileByEdition = {
      for (final f in _files) f.editionId: f,
    };
    final editions = [...book.editions];
    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: ListView(
        padding: const EdgeInsets.symmetric(vertical: 8),
        children: [
          if (_error != null) ErrorBanner(message: _error!, onRetry: _load),
          _BookHeader(book: book, onTap: _openDetail),
          const Divider(color: AppTheme.border, height: 1),
          if (editions.isEmpty)
            const Padding(
              padding: EdgeInsets.all(24),
              child: Center(
                child: Text('No editions',
                    style: TextStyle(color: AppTheme.textSecondary)),
              ),
            )
          else
            ...editions.map((e) => _EditionTile(
                  edition: e,
                  file: fileByEdition[e.id],
                  onTap: _openDetail,
                )),
        ],
      ),
    );
  }
}

/// The book summary header — cover icon, title, availability line. Tapping it
/// opens the detail sheet.
class _BookHeader extends StatelessWidget {
  final ChaptarrBook book;
  final VoidCallback onTap;

  const _BookHeader({required this.book, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final status = bookFileStatusLine(book);
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

/// One edition row: format badge, edition title and the matched file's
/// quality+size (when downloaded).
class _EditionTile extends StatelessWidget {
  final ChaptarrEdition edition;
  final ChaptarrBookFile? file;
  final VoidCallback onTap;

  const _EditionTile({
    required this.edition,
    required this.file,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final f = file;
    final fileLine = f != null
        ? '${f.qualityName ?? 'Downloaded'} — ${f.sizeFormatted}'
        : 'Not downloaded';
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
                      ChaptarrFormatBadge(format: edition.bookFormat),
                      if (edition.title != null &&
                          edition.title!.isNotEmpty) ...[
                        const SizedBox(width: 8),
                        Expanded(
                          child: Text(
                            edition.title!,
                            style: const TextStyle(
                                color: AppTheme.textPrimary,
                                fontSize: 14,
                                fontWeight: FontWeight.w500),
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                          ),
                        ),
                      ],
                    ],
                  ),
                  const SizedBox(height: 4),
                  Text(
                    fileLine,
                    style: TextStyle(
                        color: f != null
                            ? AppTheme.available
                            : AppTheme.textSecondary,
                        fontSize: 13,
                        fontWeight: FontWeight.w500),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
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
      child: Row(
        children: [
          Expanded(
            child: OutlinedButton.icon(
              onPressed: onAutomatic,
              icon:
                  const Icon(Icons.search, size: 18, color: AppTheme.available),
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
              onPressed: onInteractive,
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
    );
  }
}
