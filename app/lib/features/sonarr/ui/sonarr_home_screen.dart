import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/sonarr_api_service.dart';
import '../logic/sonarr_series_provider.dart';
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

    final backendDio = ref.read(backendClientProvider);
    final service = SonarrApiService(
      backendDio: backendDio,
      instanceId: activeInstance?.id,
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

  @override
  Widget build(BuildContext context) {
    // Rebuild when active instance changes
    ref.listen(instanceProvider.select((s) => s.activeSonarrInstanceId),
        (_, __) => _initNotifier());

    if (_notifier == null) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }

    return ListenableBuilder(
      listenable: _notifier!,
      builder: (context, _) {
        final state = _notifier!.state;

        return Column(
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
                      controller: _searchController,
                      onChanged: _notifier!.search,
                      decoration: InputDecoration(
                        hintText: 'Search series...',
                        prefixIcon: const Icon(Icons.search),
                        suffixIcon: _searchController.text.isNotEmpty
                            ? IconButton(
                                icon: const Icon(Icons.close),
                                onPressed: () {
                                  _searchController.clear();
                                  _notifier!.search('');
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
                    onSelected: _notifier!.setFilter,
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
                onRetry: _notifier!.loadSeries,
              ),

            Expanded(
              child: state.isLoading && state.series.isEmpty
                  ? const Center(
                      child:
                          CircularProgressIndicator(color: AppTheme.accent))
                  : RefreshIndicator(
                      onRefresh: _notifier!.loadSeries,
                      color: AppTheme.accent,
                      child: SonarrSeriesList(
                        series: state.filtered,
                        onDelete: (id) =>
                            _notifier!.deleteSeries(id, deleteFiles: false),
                        onSearch: _notifier!.searchForSeries,
                      ),
                    ),
            ),
          ],
        );
      },
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
        Text(count.toString(),
            style: TextStyle(
                color: color, fontSize: 20, fontWeight: FontWeight.bold)),
        Text(label,
            style:
                const TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
      ],
    );
  }
}
