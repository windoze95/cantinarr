import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/ui/category_row.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../../sonarr/data/sonarr_models.dart';
import '../../sonarr/logic/tv_discover_provider.dart';

/// Dashboard TV tab: discovery rows + simplified Sonarr library sections.
class DashboardTvTab extends ConsumerStatefulWidget {
  const DashboardTvTab({super.key});

  @override
  ConsumerState<DashboardTvTab> createState() => _DashboardTvTabState();
}

class _DashboardTvTabState extends ConsumerState<DashboardTvTab> {
  List<SonarrSeries> _recentlyDownloaded = [];
  List<SonarrSeries> _continuing = [];
  bool _isLoadingLibrary = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(tvDiscoverProvider.notifier).bootstrap();
      _loadLibraryPreview();
    });
  }

  Future<void> _loadLibraryPreview() async {
    final auth = ref.read(authProvider).valueOrNull;
    final defaultSonarr = auth?.connection?.defaultSonarrInstance;
    if (defaultSonarr == null) return;

    setState(() => _isLoadingLibrary = true);
    try {
      final backendDio = ref.read(backendClientProvider);
      final service = SonarrApiService(
          backendDio: backendDio, instanceId: defaultSonarr.id);
      final series = await service.getSeries();

      final downloaded = series
          .where((s) => s.percentComplete > 0)
          .toList()
        ..sort((a, b) =>
            b.percentComplete.compareTo(a.percentComplete));
      final continuing =
          series.where((s) => s.status == 'continuing').toList();

      setState(() {
        _recentlyDownloaded = downloaded.take(10).toList();
        _continuing = continuing.take(10).toList();
        _isLoadingLibrary = false;
      });
    } catch (_) {
      setState(() => _isLoadingLibrary = false);
    }
  }

  Future<void> _onRefresh() async {
    await Future.wait([
      ref.read(tvDiscoverProvider.notifier).bootstrap(),
      _loadLibraryPreview(),
    ]);
  }

  @override
  Widget build(BuildContext context) {
    final discover = ref.watch(tvDiscoverProvider);

    return RefreshIndicator(
      onRefresh: _onRefresh,
      color: AppTheme.accent,
      child: ListView(
        padding: const EdgeInsets.only(bottom: 24),
        children: [
          CategoryRow(
            title: 'Popular TV Shows',
            items: discover.popularTV,
            isLoading: discover.isLoadingPopular,
          ),
          if (discover.anticipated.isNotEmpty)
            CategoryRow(
              title: 'Most Anticipated',
              items: discover.anticipated,
              isLoading: discover.isLoadingAnticipated,
            ),

          if (_recentlyDownloaded.isNotEmpty || _continuing.isNotEmpty) ...[
            _SectionHeader(title: 'Your Library'),
            if (_isLoadingLibrary)
              const Padding(
                padding: EdgeInsets.all(24),
                child: Center(
                    child:
                        CircularProgressIndicator(color: AppTheme.accent)),
              ),
            if (_continuing.isNotEmpty)
              _CompactSeriesSection(
                title: 'Airing Now',
                series: _continuing,
                color: AppTheme.downloading,
              ),
            if (_recentlyDownloaded.isNotEmpty)
              _CompactSeriesSection(
                title: 'Recently Downloaded',
                series: _recentlyDownloaded,
                color: AppTheme.available,
              ),
          ],
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String title;
  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 28, 16, 8),
      child: Row(
        children: [
          Expanded(child: Container(height: 1, color: AppTheme.border)),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            child: Text(
              title,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                fontWeight: FontWeight.w600,
                letterSpacing: 0.5,
              ),
            ),
          ),
          Expanded(child: Container(height: 1, color: AppTheme.border)),
        ],
      ),
    );
  }
}

class _CompactSeriesSection extends StatelessWidget {
  final String title;
  final List<SonarrSeries> series;
  final Color color;

  const _CompactSeriesSection({
    required this.title,
    required this.series,
    required this.color,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
          child: Row(
            children: [
              Container(
                width: 8,
                height: 8,
                decoration: BoxDecoration(color: color, shape: BoxShape.circle),
              ),
              const SizedBox(width: 8),
              Text(
                '$title (${series.length})',
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
        ...series.map((s) => ListTile(
              dense: true,
              leading: Icon(Icons.tv_outlined,
                  color: color, size: 20),
              title: Text(
                s.title,
                style: const TextStyle(
                    color: AppTheme.textPrimary, fontSize: 14),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              subtitle: Text(
                '${s.year != null ? s.year.toString() : ''} ${s.status == 'continuing' ? '• Continuing' : ''}',
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 12),
              ),
            )),
      ],
    );
  }
}
