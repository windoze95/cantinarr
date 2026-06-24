import 'package:cached_network_image/cached_network_image.dart';
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
  List<ChaptarrBook> _results = [];
  bool _isSearching = false;
  bool _searched = false;
  String? _error;
  int _searchGen = 0; // guards against superseded async results

  @override
  void initState() {
    super.initState();
    _controller.addListener(() => setState(() {})); // refresh the clear button
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
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
    } catch (_) {
      if (!mounted || gen != _searchGen) return;
      setState(() {
        _isSearching = false;
        _searched = true;
        _error = 'Search failed. Please try again.';
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
            onSubmitted: (_) => _search(),
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
                  foreignId: fid, title: book.title, service: requestService)
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

  const _BookRequestButton({
    required this.foreignId,
    required this.title,
    required this.service,
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
    setState(() => _busy = true);
    final s = await widget.service
        .requestBook(foreignId: widget.foreignId, title: widget.title);
    if (!mounted) return;
    setState(() {
      _busy = false;
      if (s != null) _status = s;
    });
    if (s == null && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Request failed. Please try again.')),
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
