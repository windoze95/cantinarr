import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/library_command_header.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/lidarr_api_service.dart';
import '../data/lidarr_models.dart';
import '../logic/lidarr_library_provider.dart';
import 'lidarr_artist_list.dart';
import 'lidarr_artist_screen.dart';

/// Lidarr library management screen (the Library tab of the Lidarr module).
/// Instance-aware: uses the active Lidarr instance from the instance provider.
class LidarrHomeScreen extends ConsumerStatefulWidget {
  const LidarrHomeScreen({super.key});

  @override
  ConsumerState<LidarrHomeScreen> createState() => _LidarrHomeScreenState();
}

class _LidarrHomeScreenState extends ConsumerState<LidarrHomeScreen> {
  LidarrLibraryNotifier? _notifier;
  final _searchController = TextEditingController();

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _initNotifier());
  }

  void _initNotifier() {
    final instanceState = ref.read(instanceProvider);
    final activeInstance = instanceState.activeLidarrInstance;
    if (activeInstance == null) return;

    final backendDio = ref.read(backendClientProvider);
    final service = LidarrApiService(
      backendDio: backendDio,
      instanceId: activeInstance.id,
    );
    _notifier = LidarrLibraryNotifier(service);
    _notifier!.loadArtists();
    setState(() {});
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  Future<void> _triggerAutomaticSearch(LidarrArtist artist) async {
    try {
      await _notifier!.searchForArtist(artist.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Artist search started')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to start search: $e')));
    }
  }

  void _openArtist(LidarrArtist artist) {
    final instanceId = ref.read(instanceProvider).activeLidarrInstance?.id;
    if (instanceId == null) return;
    Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => LidarrArtistScreen(
          instanceId: instanceId,
          artistId: artist.id,
          artistName: artist.artistName,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    // Rebuild when active instance changes
    ref.listen(instanceProvider.select((s) => s.activeLidarrInstanceId),
        (_, __) => _initNotifier());

    if (_notifier == null) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }

    return ListenableBuilder(
      listenable: _notifier!,
      builder: (context, _) {
        final state = _notifier!.state;
        final instanceName =
            ref.watch(instanceProvider).activeLidarrInstance?.name ?? 'Lidarr';

        return Column(
          children: [
            LibraryCommandHeader(
              title: 'Artist library',
              subtitle: '$instanceName  /  Lidarr',
              stats: [
                LibraryStat(
                  label: 'Total',
                  value: state.artists.length,
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
              searchHint: 'Filter this artist library…',
              filter: PopupMenuButton<LidarrLibraryFilter>(
                tooltip: 'Filter artists',
                icon: const Icon(Icons.tune_rounded),
                onSelected: _notifier!.setFilter,
                itemBuilder: (_) => LidarrLibraryFilter.values
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
            if (state.error != null)
              ErrorBanner(
                message: state.error!,
                onRetry: _notifier!.loadArtists,
              ),
            Expanded(
              child: state.isLoading && state.artists.isEmpty
                  ? const Center(
                      child: CircularProgressIndicator(color: AppTheme.accent))
                  : RefreshIndicator(
                      onRefresh: _notifier!.loadArtists,
                      color: AppTheme.accent,
                      child: LidarrArtistList(
                        artists: state.filtered,
                        onTap: _openArtist,
                        onSearch: _triggerAutomaticSearch,
                      ),
                    ),
            ),
          ],
        );
      },
    );
  }
}
