import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/storage/library_view_preferences.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/library_actions.dart';
import '../../../core/widgets/library_command_header.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import '../logic/sonarr_series_provider.dart';
import 'series_actions.dart';
import 'sonarr_series_detail_screen.dart';
import 'sonarr_series_list.dart';

/// Sonarr library management screen (used in the Sonarr module).
/// Instance-aware: uses the active Sonarr instance from the instance provider.
class SonarrHomeScreen extends ConsumerStatefulWidget {
  const SonarrHomeScreen({super.key});

  @override
  ConsumerState<SonarrHomeScreen> createState() => _SonarrHomeScreenState();
}

class _SonarrHomeScreenState extends ConsumerState<SonarrHomeScreen> {
  SonarrSeriesNotifier? _notifier;
  final _searchController = TextEditingController();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _initNotifier());
  }

  void _initNotifier() {
    final instanceState = ref.read(instanceProvider);
    final activeInstance = instanceState.activeSonarrInstance;
    if (activeInstance == null) return;

    final backendDio = ref.read(backendClientProvider);
    final service = SonarrApiService(
      backendDio: backendDio,
      instanceId: activeInstance.id,
    );
    _notifier = SonarrSeriesNotifier(service);
    _notifier!.loadSeries();
    setState(() {});
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _openSeries(SonarrSeries show) async {
    final instanceId = ref.read(instanceProvider).activeSonarrInstance?.id;
    if (instanceId == null) return;
    final notifier = _notifier;
    await Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => SonarrSeriesDetailScreen(
          instanceId: instanceId,
          series: show,
        ),
      ),
    );
    // The detail screen can edit or remove the series; refresh on return.
    if (mounted && identical(_notifier, notifier)) notifier?.loadSeries();
  }

  /// Tile and detail menus run the same actions.
  void _showSeriesActions(SonarrSeries show, LibraryAction action) {
    final instanceId = ref.read(instanceProvider).activeSonarrInstance?.id;
    if (instanceId == null) return;
    final notifier = _notifier;
    void reload() {
      if (mounted && identical(_notifier, notifier)) {
        notifier?.loadSeries();
      }
    }
    showSeriesActions(
      context,
      service: SonarrApiService(
        backendDio: ref.read(backendClientProvider),
        instanceId: instanceId,
      ),
      instanceId: instanceId,
      series: show,
      selectedAction: action,
      onChanged: reload,
      onRemoved: reload,
    );
  }

  @override
  Widget build(BuildContext context) {
    // Rebuild when active instance changes
    ref.listen(instanceProvider.select((s) => s.activeSonarrInstanceId),
        (_, __) => _initNotifier());

    if (_notifier == null) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }

    final viewMode = ref.watch(libraryViewModeProvider('sonarr'));
    final instanceId = ref.watch(instanceProvider).activeSonarrInstance?.id;

    return ListenableBuilder(
      listenable: _notifier!,
      builder: (context, _) {
        final state = _notifier!.state;
        final instanceName =
            ref.watch(instanceProvider).activeSonarrInstance?.name ?? 'Sonarr';

        return LibraryCommandLayout(
          key: ValueKey('sonarr-$instanceId'),
          headerBuilder: (collapsed) => LibraryCommandHeader(
              collapsed: collapsed,
              viewMode: viewMode,
              onViewModeChanged: (value) => ref
                  .read(libraryViewModeProvider('sonarr').notifier).set(value),
              title: 'Series library',
              subtitle: '$instanceName  /  Sonarr',
              stats: [
                LibraryStat(
                  label: 'Total',
                  value: state.series.length,
                  color: AppTheme.textPrimary,
                ),
                LibraryStat(
                  label: 'Complete',
                  value: state.completeCount,
                  color: AppTheme.available,
                ),
                LibraryStat(
                  label: 'Partial',
                  value: state.partialCount,
                  color: AppTheme.requested,
                ),
              ],
              searchController: _searchController,
              onSearch: _notifier!.search,
              searchHint: 'Filter this series library…',
              filter: PopupMenuButton<SonarrFilter>(
                tooltip: 'Filter series',
                icon: const Icon(Icons.tune_rounded),
                onSelected: _notifier!.setFilter,
                itemBuilder: (_) => SonarrFilter.values
                    .map((f) => PopupMenuItem(
                          value: f,
                          child: Row(
                            children: [
                              if (f == state.filter)
                                const Icon(
                                  Icons.check,
                                  size: 18,
                                  color: AppTheme.accent,
                                ),
                              if (f != state.filter) const SizedBox(width: 18),
                              const SizedBox(width: 8),
                              Text(
                                f.name[0].toUpperCase() + f.name.substring(1),
                              ),
                            ],
                          ),
                        ))
                    .toList(),
              ),
            ),
          children: [
            if (state.error != null)
              ErrorBanner(
                message: state.error!,
                onRetry: _notifier!.loadSeries,
              ),
            Expanded(
              child: state.isLoading && state.series.isEmpty
                  ? const Center(
                      child: CircularProgressIndicator(color: AppTheme.accent))
                  : state.error != null && state.series.isEmpty
                      ? const SizedBox.shrink()
                      : RefreshIndicator(
                          onRefresh: _notifier!.loadSeries,
                          color: AppTheme.accent,
                          child: SonarrSeriesList(
                            viewMode: viewMode,
                            scrollKey: 'sonarr-$instanceId',
                            series: state.filtered,
                            onOpen: _openSeries,
                            onAction: _showSeriesActions,
                          ),
                        ),
            ),
          ],
        );
      },
    );
  }
}
