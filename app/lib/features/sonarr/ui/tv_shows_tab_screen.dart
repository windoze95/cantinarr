import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/ui/category_row.dart';
import '../data/sonarr_api_service.dart';
import '../logic/sonarr_series_provider.dart';
import '../logic/tv_discover_provider.dart';
import 'sonarr_series_list.dart';

/// Composite TV Shows tab: discovery rows + Sonarr library.
class TvShowsTabScreen extends ConsumerStatefulWidget {
  const TvShowsTabScreen({super.key});

  @override
  ConsumerState<TvShowsTabScreen> createState() => _TvShowsTabScreenState();
}

class _TvShowsTabScreenState extends ConsumerState<TvShowsTabScreen> {
  SonarrSeriesNotifier? _libraryNotifier;
  final _searchController = TextEditingController();
  bool _hasSonarr = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(tvDiscoverProvider.notifier).bootstrap();
      _initLibrary();
    });
  }

  void _initLibrary() {
    final auth = ref.read(authProvider).valueOrNull;
    final defaultSonarr = auth?.connection?.defaultSonarrInstance;
    _hasSonarr = defaultSonarr != null;
    if (_hasSonarr) {
      final backendDio = ref.read(backendClientProvider);
      final service = SonarrApiService(
        backendDio: backendDio,
        instanceId: defaultSonarr!.id,
      );
      _libraryNotifier = SonarrSeriesNotifier(service);
      _libraryNotifier!.loadSeries();
      setState(() {});
    }
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _onRefresh() async {
    await Future.wait([
      ref.read(tvDiscoverProvider.notifier).bootstrap(),
      if (_libraryNotifier != null) _libraryNotifier!.loadSeries(),
    ]);
  }

  @override
  Widget build(BuildContext context) {
    final discover = ref.watch(tvDiscoverProvider);

    return Scaffold(
      body: SafeArea(
        child: RefreshIndicator(
          onRefresh: _onRefresh,
          color: AppTheme.accent,
          child: ListView(
            padding: const EdgeInsets.only(bottom: 24),
            children: [
              // Discovery rows
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

              // Library section
              _SectionHeader(title: 'Your Library'),

              if (_hasSonarr && _libraryNotifier != null)
                _LibrarySection(
                  notifier: _libraryNotifier!,
                  searchController: _searchController,
                )
              else
                _UnconfiguredPlaceholder(
                  icon: Icons.tv_outlined,
                  message: 'Sonarr is not configured on this server.',
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _LibrarySection extends StatelessWidget {
  final SonarrSeriesNotifier notifier;
  final TextEditingController searchController;

  const _LibrarySection({
    required this.notifier,
    required this.searchController,
  });

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: notifier,
      builder: (context, _) {
        final state = notifier.state;
        return Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            // Stats bar
            Container(
              padding:
                  const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              color: AppTheme.surface,
              child: Row(
                mainAxisAlignment: MainAxisAlignment.spaceAround,
                children: [
                  _StatChip(
                      label: 'Total',
                      count: state.series.length,
                      color: AppTheme.textPrimary),
                  _StatChip(
                      label: 'Complete',
                      count: state.completeCount,
                      color: AppTheme.available),
                  _StatChip(
                      label: 'Partial',
                      count: state.partialCount,
                      color: AppTheme.requested),
                ],
              ),
            ),

            // Search + filter
            Padding(
              padding: const EdgeInsets.all(12),
              child: Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: searchController,
                      onChanged: notifier.search,
                      decoration: InputDecoration(
                        hintText: 'Search series...',
                        prefixIcon: const Icon(Icons.search),
                        suffixIcon: searchController.text.isNotEmpty
                            ? IconButton(
                                icon: const Icon(Icons.close),
                                onPressed: () {
                                  searchController.clear();
                                  notifier.search('');
                                },
                              )
                            : null,
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  PopupMenuButton<SonarrFilter>(
                    icon: const Icon(Icons.filter_list,
                        color: AppTheme.textPrimary),
                    onSelected: notifier.setFilter,
                    itemBuilder: (_) => SonarrFilter.values
                        .map((f) => PopupMenuItem(
                              value: f,
                              child: Row(
                                children: [
                                  if (f == state.filter)
                                    const Icon(Icons.check,
                                        size: 18, color: AppTheme.accent),
                                  if (f != state.filter)
                                    const SizedBox(width: 18),
                                  const SizedBox(width: 8),
                                  Text(f.name[0].toUpperCase() +
                                      f.name.substring(1)),
                                ],
                              ),
                            ))
                        .toList(),
                  ),
                ],
              ),
            ),

            if (state.error != null)
              ErrorBanner(
                message: state.error!,
                onRetry: notifier.loadSeries,
              ),

            if (state.isLoading && state.series.isEmpty)
              const Padding(
                padding: EdgeInsets.all(32),
                child: Center(
                  child:
                      CircularProgressIndicator(color: AppTheme.accent),
                ),
              )
            else
              SonarrSeriesList(
                series: state.filtered,
                onDelete: (id) =>
                    notifier.deleteSeries(id, deleteFiles: false),
                onSearch: notifier.searchForSeries,
                embedded: true,
              ),
          ],
        );
      },
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
          Expanded(
            child: Container(height: 1, color: AppTheme.border),
          ),
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
          Expanded(
            child: Container(height: 1, color: AppTheme.border),
          ),
        ],
      ),
    );
  }
}

class _UnconfiguredPlaceholder extends StatelessWidget {
  final IconData icon;
  final String message;

  const _UnconfiguredPlaceholder({
    required this.icon,
    required this.message,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(32),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 48, color: AppTheme.textSecondary),
          const SizedBox(height: 12),
          Text(
            message,
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 14),
            textAlign: TextAlign.center,
          ),
        ],
      ),
    );
  }
}

class _StatChip extends StatelessWidget {
  final String label;
  final int count;
  final Color color;

  const _StatChip({
    required this.label,
    required this.count,
    required this.color,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          count.toString(),
          style: TextStyle(
              color: color, fontSize: 20, fontWeight: FontWeight.bold),
        ),
        Text(label,
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 12)),
      ],
    );
  }
}
