import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';
import 'radarr_releases_screen.dart';

enum _WantedView { missing, cutoff }

/// Paginated Radarr wanted movies: missing files and cutoff-unmet quality.
class RadarrWantedScreen extends ConsumerStatefulWidget {
  const RadarrWantedScreen({super.key});

  @override
  ConsumerState<RadarrWantedScreen> createState() => _RadarrWantedScreenState();
}

class _RadarrWantedScreenState extends ConsumerState<RadarrWantedScreen> {
  static const _pageSize = 50;

  final _scrollController = ScrollController();
  _WantedView _view = _WantedView.missing;
  List<RadarrMovie> _records = [];
  int _totalRecords = 0;
  int _page = 1;
  bool _isLoading = true;
  bool _isLoadingMore = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _scrollController.addListener(_onScroll);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    _scrollController.dispose();
    super.dispose();
  }

  void _onScroll() {
    if (_scrollController.position.extentAfter < 400) _loadMore();
  }

  RadarrApiService? _buildService() {
    final instanceId = ref.read(instanceProvider).activeRadarrInstance?.id;
    if (instanceId == null) return null;
    return RadarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  Future<RadarrWantedPage> _fetch(RadarrApiService service, int page) {
    return _view == _WantedView.missing
        ? service.getWantedMissing(page: page, pageSize: _pageSize)
        : service.getWantedCutoff(page: page, pageSize: _pageSize);
  }

  Future<void> _load() async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Radarr instance configured';
      });
      return;
    }

    final view = _view;
    setState(() => _isLoading = true);
    try {
      final page = await _fetch(service, 1);
      if (!mounted || view != _view) return;
      setState(() {
        _records = page.records;
        _totalRecords = page.totalRecords;
        _page = 1;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted || view != _view) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load wanted movies: $e';
      });
    }
  }

  Future<void> _loadMore() async {
    if (_isLoading || _isLoadingMore || !_hasMore) return;
    final service = _buildService();
    if (service == null) return;

    final view = _view;
    setState(() => _isLoadingMore = true);
    try {
      final next = await _fetch(service, _page + 1);
      if (!mounted || view != _view) return;
      setState(() {
        _page += 1;
        _records = [..._records, ...next.records];
        _totalRecords = next.totalRecords;
        _isLoadingMore = false;
      });
    } catch (_) {
      if (!mounted || view != _view) return;
      setState(() => _isLoadingMore = false);
    }
  }

  bool get _hasMore => _records.length < _totalRecords;

  void _onViewChanged(_WantedView view) {
    if (view == _view) return;
    setState(() {
      _view = view;
      _records = [];
      _totalRecords = 0;
      _page = 1;
      _error = null;
    });
    _load();
  }

  Future<void> _automaticSearch(RadarrMovie movie) async {
    final service = _buildService();
    if (service == null) return;
    try {
      await service.searchMovie(movie.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Search started for "${movie.title}"')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to start search: $e')));
    }
  }

  void _openInteractiveSearch(RadarrMovie movie) {
    final instanceId = ref.read(instanceProvider).activeRadarrInstance?.id;
    if (instanceId == null) return;
    Navigator.of(context, rootNavigator: true).push(
      MaterialPageRoute(
        builder: (_) => RadarrReleasesScreen(
          instanceId: instanceId,
          movieId: movie.id,
          movieTitle: movie.title,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    // Reload when the active instance changes.
    ref.listen(instanceProvider.select((s) => s.activeRadarrInstanceId),
        (_, __) => _load());

    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: SegmentedButton<_WantedView>(
            showSelectedIcon: false,
            segments: const [
              ButtonSegment(value: _WantedView.missing, label: Text('Missing')),
              ButtonSegment(
                  value: _WantedView.cutoff, label: Text('Cutoff Unmet')),
            ],
            selected: {_view},
            onSelectionChanged: (value) => _onViewChanged(value.first),
          ),
        ),
        Expanded(child: _buildBody()),
      ],
    );
  }

  Widget _buildBody() {
    if (_isLoading && _records.isEmpty) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null && _records.isEmpty) {
      return FullScreenError(message: _error!, onRetry: _load);
    }
    if (_records.isEmpty) {
      return RefreshIndicator(
        onRefresh: _load,
        color: AppTheme.accent,
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          children: [
            const SizedBox(height: 160),
            const Icon(Icons.check_circle_outline,
                size: 48, color: AppTheme.available),
            const SizedBox(height: 12),
            Center(
              child: Text(
                _view == _WantedView.missing
                    ? 'Nothing missing — library is healthy'
                    : 'No cutoff unmet movies — quality goals met',
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 16),
              ),
            ),
          ],
        ),
      );
    }

    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: ListView.builder(
        controller: _scrollController,
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: _records.length + (_hasMore ? 1 : 0),
        itemBuilder: (context, index) {
          if (index >= _records.length) {
            return const Padding(
              padding: EdgeInsets.all(16),
              child: Center(
                child: SizedBox(
                  width: 24,
                  height: 24,
                  child: CircularProgressIndicator(
                      color: AppTheme.accent, strokeWidth: 2.5),
                ),
              ),
            );
          }

          final movie = _records[index];
          return _WantedTile(
            movie: movie,
            showQuality: _view == _WantedView.cutoff,
            onAutomaticSearch: () => _automaticSearch(movie),
            onInteractiveSearch: () => _openInteractiveSearch(movie),
          );
        },
      ),
    );
  }
}

String _releaseLabel(RadarrMovie movie) {
  final format = DateFormat('MMM d, yyyy');
  if (movie.digitalRelease != null) {
    return 'Digital ${format.format(movie.digitalRelease!.toLocal())}';
  }
  if (movie.physicalRelease != null) {
    return 'Physical ${format.format(movie.physicalRelease!.toLocal())}';
  }
  if (movie.inCinemas != null) {
    return 'In cinemas ${format.format(movie.inCinemas!.toLocal())}';
  }
  return movie.status ?? 'Unreleased';
}

class _WantedTile extends StatelessWidget {
  final RadarrMovie movie;
  final bool showQuality;
  final VoidCallback onAutomaticSearch;
  final VoidCallback onInteractiveSearch;

  const _WantedTile({
    required this.movie,
    required this.showQuality,
    required this.onAutomaticSearch,
    required this.onInteractiveSearch,
  });

  @override
  Widget build(BuildContext context) {
    final subtitleParts = [
      if (showQuality && movie.movieFile?.quality != null)
        'Current: ${movie.movieFile!.quality}',
      _releaseLabel(movie),
    ];

    return ListTile(
      contentPadding: const EdgeInsets.only(left: 16, right: 4),
      title: Text(
        movie.year > 0 ? '${movie.title} (${movie.year})' : movie.title,
        style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 13.5,
            fontWeight: FontWeight.w500),
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        subtitleParts.join(' • '),
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          IconButton(
            icon: const Icon(Icons.search, color: AppTheme.accent, size: 22),
            tooltip: 'Automatic search',
            onPressed: onAutomaticSearch,
          ),
          IconButton(
            icon: const Icon(Icons.manage_search,
                color: AppTheme.textSecondary, size: 22),
            tooltip: 'Interactive search',
            onPressed: onInteractiveSearch,
          ),
        ],
      ),
    );
  }
}
