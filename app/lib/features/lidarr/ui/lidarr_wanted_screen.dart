import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/lidarr_api_service.dart';
import '../data/lidarr_models.dart';

enum _WantedView { missing, cutoff }

/// Monitored albums Lidarr wants: missing entirely, or below their quality
/// cutoff. Each row offers an automatic search.
class LidarrWantedScreen extends ConsumerStatefulWidget {
  const LidarrWantedScreen({super.key});

  @override
  ConsumerState<LidarrWantedScreen> createState() => _LidarrWantedScreenState();
}

class _LidarrWantedScreenState extends ConsumerState<LidarrWantedScreen> {
  static const _pageSize = 50;

  final _scrollController = ScrollController();
  _WantedView _view = _WantedView.missing;
  List<LidarrAlbum> _records = [];
  int _totalRecords = 0;
  int _page = 1;
  bool _isLoading = true;
  bool _isLoadingMore = false;
  String? _error;
  int _loadGeneration = 0;

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

  LidarrApiService? _buildService() {
    final instanceId = ref.read(instanceProvider).activeLidarrInstance?.id;
    if (instanceId == null) return null;
    return LidarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  Future<LidarrWantedPage> _fetch(LidarrApiService service, int page) =>
      _view == _WantedView.missing
          ? service.getWantedMissing(page: page, pageSize: _pageSize)
          : service.getWantedCutoff(page: page, pageSize: _pageSize);

  Future<void> _load() async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Lidarr instance configured';
      });
      return;
    }

    final gen = ++_loadGeneration;
    setState(() => _isLoading = true);
    try {
      final page = await _fetch(service, 1);
      if (!mounted || gen != _loadGeneration) return;
      setState(() {
        _records = page.records;
        _totalRecords = page.totalRecords;
        _page = 1;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted || gen != _loadGeneration) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load wanted albums: $e';
      });
    }
  }

  Future<void> _loadMore() async {
    if (_isLoading || _isLoadingMore || !_hasMore) return;
    final service = _buildService();
    if (service == null) return;

    final view = _view;
    final gen = _loadGeneration;
    setState(() => _isLoadingMore = true);
    try {
      final next = await _fetch(service, _page + 1);
      if (!mounted || gen != _loadGeneration || view != _view) return;
      setState(() {
        _page += 1;
        _records = [..._records, ...next.records];
        _totalRecords = next.totalRecords;
        _isLoadingMore = false;
      });
    } catch (_) {
      if (!mounted || gen != _loadGeneration || view != _view) return;
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

  Future<void> _automaticSearch(LidarrAlbum album) async {
    final service = _buildService();
    if (service == null) return;
    try {
      await service.searchAlbums([album.id]);
      if (!mounted) return;
      final label = [
        if (album.artistName.isNotEmpty) album.artistName,
        album.title,
      ].join(' — ');
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content:
              Text('Search started${label.isNotEmpty ? ' for $label' : ''}')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to start search: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    // Reload when the active instance changes.
    ref.listen(instanceProvider.select((s) => s.activeLidarrInstanceId),
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
                    : 'No cutoff unmet albums — quality goals met',
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

          final album = _records[index];
          return _WantedTile(
            album: album,
            onAutomaticSearch: () => _automaticSearch(album),
          );
        },
      ),
    );
  }
}

String _releaseDateLabel(DateTime? releaseDate) {
  if (releaseDate == null) return 'No release date';
  final local = releaseDate.toLocal();
  final now = DateTime.now();
  if (local.year == now.year) return DateFormat('MMM d').format(local);
  return DateFormat('MMM d, yyyy').format(local);
}

class _WantedTile extends StatelessWidget {
  final LidarrAlbum album;
  final VoidCallback onAutomaticSearch;

  const _WantedTile({
    required this.album,
    required this.onAutomaticSearch,
  });

  @override
  Widget build(BuildContext context) {
    final subtitleParts = [
      album.title,
      _releaseDateLabel(album.releaseDate),
    ];

    return ListTile(
      contentPadding: const EdgeInsets.only(left: 16, right: 4),
      title: Text(
        album.artistName.isNotEmpty ? album.artistName : album.title,
        style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 13.5,
            fontWeight: FontWeight.w500),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        subtitleParts.join(' • '),
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: IconButton(
        icon: const Icon(Icons.search, color: AppTheme.accent, size: 22),
        tooltip: 'Find automatically',
        onPressed: onAutomaticSearch,
      ),
    );
  }
}
