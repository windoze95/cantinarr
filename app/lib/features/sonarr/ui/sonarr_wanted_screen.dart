import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import 'sonarr_releases_screen.dart';

enum _WantedView { missing, cutoff }

/// Paginated Sonarr wanted episodes: missing files and cutoff-unmet quality.
class SonarrWantedScreen extends ConsumerStatefulWidget {
  const SonarrWantedScreen({super.key});

  @override
  ConsumerState<SonarrWantedScreen> createState() => _SonarrWantedScreenState();
}

class _SonarrWantedScreenState extends ConsumerState<SonarrWantedScreen> {
  static const _pageSize = 50;

  final _scrollController = ScrollController();
  _WantedView _view = _WantedView.missing;
  List<SonarrWantedRecord> _records = [];
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

  SonarrApiService? _buildService() {
    final instanceId = ref.read(instanceProvider).activeSonarrInstance?.id;
    if (instanceId == null) return null;
    return SonarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  Future<SonarrWantedPage> _fetch(SonarrApiService service, int page) {
    return _view == _WantedView.missing
        ? service.getWantedMissing(page: page, pageSize: _pageSize)
        : service.getWantedCutoff(page: page, pageSize: _pageSize);
  }

  Future<void> _load() async {
    final service = _buildService();
    if (service == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Sonarr instance configured';
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
        _error = 'Failed to load wanted episodes: $e';
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

  Future<void> _automaticSearch(SonarrWantedRecord record) async {
    final service = _buildService();
    if (service == null) return;
    try {
      await service.searchEpisodes([record.id]);
      if (!mounted) return;
      final label = [
        if (record.seriesTitle != null) record.seriesTitle,
        record.seasonEpisodeLabel,
      ].join(' ');
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Search started for $label')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to start search: $e')));
    }
  }

  void _openInteractiveSearch(SonarrWantedRecord record) {
    final instanceId = ref.read(instanceProvider).activeSonarrInstance?.id;
    if (instanceId == null) return;
    // The interactive search is season-scoped; pass the episode's season.
    Navigator.of(context, rootNavigator: true).push(
      MaterialPageRoute(
        builder: (_) => SonarrReleasesScreen(
          instanceId: instanceId,
          seriesId: record.seriesId,
          seasonNumber: record.seasonNumber,
          seriesTitle: record.seriesTitle ?? 'Series',
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    // Reload when the active instance changes.
    ref.listen(instanceProvider.select((s) => s.activeSonarrInstanceId),
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
                    : 'No cutoff unmet episodes — quality goals met',
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

          final record = _records[index];
          return _WantedTile(
            record: record,
            showQuality: _view == _WantedView.cutoff,
            onAutomaticSearch: () => _automaticSearch(record),
            onInteractiveSearch: () => _openInteractiveSearch(record),
          );
        },
      ),
    );
  }
}

String _airDateLabel(DateTime? airDateUtc) {
  if (airDateUtc == null) return 'Unaired';
  final local = airDateUtc.toLocal();
  final now = DateTime.now();
  if (local.year == now.year) return DateFormat('MMM d').format(local);
  return DateFormat('MMM d, yyyy').format(local);
}

class _WantedTile extends StatelessWidget {
  final SonarrWantedRecord record;
  final bool showQuality;
  final VoidCallback onAutomaticSearch;
  final VoidCallback onInteractiveSearch;

  const _WantedTile({
    required this.record,
    required this.showQuality,
    required this.onAutomaticSearch,
    required this.onInteractiveSearch,
  });

  @override
  Widget build(BuildContext context) {
    final subtitleParts = [
      [
        record.seasonEpisodeLabel,
        if (record.title != null && record.title!.isNotEmpty) record.title!,
      ].join(' • '),
      if (showQuality && record.quality != null) 'Current: ${record.quality}',
      _airDateLabel(record.airDateUtc),
    ];

    return ListTile(
      contentPadding: const EdgeInsets.only(left: 16, right: 4),
      title: Text(
        record.seriesTitle ?? 'Unknown series',
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
