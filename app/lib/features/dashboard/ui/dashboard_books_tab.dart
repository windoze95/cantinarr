import 'dart:async';

import 'package:cached_network_image/cached_network_image.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../chaptarr/data/chaptarr_api_service.dart';
import '../../chaptarr/data/chaptarr_models.dart';
import '../../request/data/request_service.dart';

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
    if (_results.isEmpty) {
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
    return ListView.separated(
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemCount: _results.length,
      separatorBuilder: (_, __) =>
          const Divider(height: 1, color: AppTheme.border),
      itemBuilder: (_, i) =>
          _BookResultTile(book: _results[i], requestService: requestService),
    );
  }
}

class _BookResultTile extends StatelessWidget {
  final ChaptarrBook book;
  final RequestService requestService;

  const _BookResultTile({required this.book, required this.requestService});

  @override
  Widget build(BuildContext context) {
    final year = book.releaseDate?.year;
    final subtitle = <String>[
      if (book.author?.authorName.isNotEmpty ?? false) book.author!.authorName,
      if (year != null) '$year',
    ].join(' · ');
    final fid = book.foreignBookId;

    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      leading: SizedBox(
        width: 44,
        height: 66,
        child: ClipRRect(
          borderRadius: BorderRadius.circular(4),
          child: book.coverUrl != null
              ? CachedNetworkImage(
                  imageUrl: book.coverUrl!,
                  fit: BoxFit.cover,
                  placeholder: (_, __) =>
                      Container(color: AppTheme.surfaceVariant),
                  errorWidget: (_, __, ___) => Container(
                    color: AppTheme.surfaceVariant,
                    child: const Icon(Icons.menu_book,
                        color: AppTheme.textSecondary, size: 20),
                  ),
                )
              : Container(
                  color: AppTheme.surfaceVariant,
                  child: const Icon(Icons.menu_book,
                      color: AppTheme.textSecondary, size: 20),
                ),
        ),
      ),
      title: Text(
        book.title,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
        style: const TextStyle(
            color: AppTheme.textPrimary, fontWeight: FontWeight.w600),
      ),
      subtitle: subtitle.isEmpty
          ? null
          : Text(subtitle,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(color: AppTheme.textSecondary)),
      // A Chaptarr lookup result that is already tracked in the library comes
      // back with a non-zero id; show that instead of a Request button (which
      // would otherwise try to re-add it).
      trailing: book.id != 0
          ? const Padding(
              padding: EdgeInsets.only(right: 8),
              child: Text(
                'In Library',
                style: TextStyle(
                  color: AppTheme.available,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
            )
          : (fid != null && fid.isNotEmpty)
              ? _BookRequestButton(
                  foreignId: fid,
                  title: book.title,
                  service: requestService,
                  availableFormats: book.formats,
                )
              : null,
    );
  }
}

/// Per-book request affordance: loads the user's request state on build, and on
/// tap submits a request (which may land as pending when approval is required).
class _BookRequestButton extends StatefulWidget {
  final String foreignId;
  final String title;
  final RequestService service;
  final Set<BookFormat> availableFormats;

  const _BookRequestButton({
    required this.foreignId,
    required this.title,
    required this.service,
    required this.availableFormats,
  });

  @override
  State<_BookRequestButton> createState() => _BookRequestButtonState();
}

class _BookRequestButtonState extends State<_BookRequestButton> {
  RequestStatus _status = RequestStatus.unavailable;
  bool _loading = true;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    _check();
  }

  Future<void> _check() async {
    final s = await widget.service.checkBookStatus(widget.foreignId);
    if (!mounted) return;
    setState(() {
      _status = s;
      _loading = false;
    });
  }

  Future<void> _request() async {
    if (_busy) return;
    final choices = _formatChoices(widget.availableFormats);
    BookRequestFormat format =
        choices.length == 1 ? choices.first : BookRequestFormat.both;
    if (choices.length > 1) {
      final selected = await showModalBottomSheet<BookRequestFormat>(
        context: context,
        backgroundColor: Colors.transparent,
        builder: (_) => _BookFormatSheet(
          title: widget.title,
          choices: choices,
        ),
      );
      if (selected == null) return;
      format = selected;
    }
    if (!mounted) return;
    setState(() => _busy = true);
    RequestStatus? s;
    String? failureMessage;
    try {
      s = await widget.service.requestBook(
        foreignId: widget.foreignId,
        title: widget.title,
        format: format,
      );
    } on RequestSubmissionException catch (e) {
      failureMessage = e.message;
    }
    if (!mounted) return;
    setState(() {
      _busy = false;
      if (s != null) _status = s;
    });
    if (s == null && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(failureMessage ?? 'Request failed. Please try again.'),
        ),
      );
    }
  }

  bool get _requestable =>
      _status == RequestStatus.unavailable || _status == RequestStatus.denied;

  Color get _color => switch (_status) {
        RequestStatus.pending ||
        RequestStatus.requested ||
        RequestStatus.partial =>
          AppTheme.requested,
        RequestStatus.downloading => AppTheme.downloading,
        RequestStatus.available => AppTheme.available,
        _ => AppTheme.accent,
      };

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const SizedBox(
        width: 96,
        child: Center(
          child: SizedBox(
            width: 16,
            height: 16,
            child: CircularProgressIndicator(
                strokeWidth: 2, color: AppTheme.accent),
          ),
        ),
      );
    }
    return TextButton(
      onPressed: _requestable && !_busy ? _request : null,
      style: TextButton.styleFrom(foregroundColor: _color),
      child: _busy
          ? const SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : Text(_status.buttonLabel),
    );
  }
}

List<BookRequestFormat> _formatChoices(Set<BookFormat> available) {
  final hasEbook = available.contains(BookFormat.ebook);
  final hasAudiobook = available.contains(BookFormat.audiobook);
  if (hasEbook && hasAudiobook) {
    return const [
      BookRequestFormat.ebook,
      BookRequestFormat.audiobook,
      BookRequestFormat.both,
    ];
  }
  if (hasEbook) return const [BookRequestFormat.ebook];
  if (hasAudiobook) return const [BookRequestFormat.audiobook];
  return const [
    BookRequestFormat.ebook,
    BookRequestFormat.audiobook,
    BookRequestFormat.both,
  ];
}

class _BookFormatSheet extends StatelessWidget {
  final String title;
  final List<BookRequestFormat> choices;

  const _BookFormatSheet({
    required this.title,
    required this.choices,
  });

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Container(
        padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
        decoration: const BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        ),
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
            const SizedBox(height: 18),
            Text(
              title,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 18,
                fontWeight: FontWeight.bold,
              ),
            ),
            const SizedBox(height: 14),
            for (final choice in choices)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: _FormatChoiceTile(choice: choice),
              ),
          ],
        ),
      ),
    );
  }
}

class _FormatChoiceTile extends StatelessWidget {
  final BookRequestFormat choice;

  const _FormatChoiceTile({required this.choice});

  @override
  Widget build(BuildContext context) {
    final icon = switch (choice) {
      BookRequestFormat.ebook => Icons.menu_book,
      BookRequestFormat.audiobook => Icons.headphones,
      BookRequestFormat.both => Icons.library_books,
    };
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 12),
      leading: Icon(icon, color: AppTheme.accent),
      title: Text(
        choice.label,
        style: const TextStyle(
          color: AppTheme.textPrimary,
          fontWeight: FontWeight.w600,
        ),
      ),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(8),
        side: const BorderSide(color: AppTheme.border),
      ),
      onTap: () => Navigator.of(context).pop(choice),
    );
  }
}
