import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/storage/library_sort_preferences.dart';
import '../../../core/widgets/library_sort_menu.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/network/library_settings_service.dart';
import '../../../core/widgets/library_actions.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/storage/library_view_preferences.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/library_command_header.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/lidarr_api_service.dart';
import '../data/lidarr_models.dart';
import '../data/lidarr_image.dart';
import '../logic/lidarr_library_provider.dart';
import 'artist_actions.dart';
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
    // Listen before the first frame so preference restoration cannot race the
    // instance notifier's creation. Its initial selection is also read below.
    ref.listenManual(librarySortProvider('lidarr'), (_, selection) =>
        _notifier?.sorting.setSelection(selection));
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
    _notifier?.dispose();
    _notifier = LidarrLibraryNotifier(service);
    _notifier!.sorting.setSelection(ref.read(librarySortProvider('lidarr')));
    _notifier!.loadArtists();
    setState(() {});
  }

  @override
  void dispose() {
    _notifier?.dispose();
    _searchController.dispose();
    super.dispose();
  }

  void _showActions(LidarrArtist artist, LibraryAction action) {
    final instanceId = ref.read(instanceProvider).activeLidarrInstance?.id;
    if (instanceId == null) return;
    final notifier = _notifier;
    final dio = ref.read(backendClientProvider);
    void reload() {
      if (mounted && identical(_notifier, notifier)) {
        notifier?.loadArtists();
      }
    }
    showArtistActions(context,
      service: LidarrApiService(backendDio: dio, instanceId: instanceId),
      settings: LibrarySettingsService(dio: dio, instanceId: instanceId,
        kind: LibrarySettingsKind.artist, id: artist.id),
      instanceId: instanceId, artist: artist, selectedAction: action,
      onChanged: reload, onRemoved: reload);
  }

  Future<void> _openArtist(LidarrArtist artist) async {
    final instanceId = ref.read(instanceProvider).activeLidarrInstance?.id;
    if (instanceId == null) return;
    final notifier = _notifier;
    await Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => LidarrArtistScreen(
          instanceId: instanceId,
          artistId: artist.id,
          artistName: artist.artistName,
        ),
      ),
    );
    if (mounted && identical(_notifier, notifier)) notifier?.loadArtists();
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

    final sort = ref.watch(librarySortProvider('lidarr'));
    final viewMode = ref.watch(libraryViewModeProvider('lidarr'));
    final instanceId = ref.watch(instanceProvider).activeLidarrInstance?.id;

    return ListenableBuilder(
      listenable: _notifier!,
      builder: (context, _) {
        final state = _notifier!.state;
        final instanceName =
            ref.watch(instanceProvider).activeLidarrInstance?.name ?? 'Lidarr';

        return LibraryCommandLayout(
          key: ValueKey('lidarr-$instanceId'),
          headerBuilder: (collapsed) => LibraryCommandHeader(
              collapsed: collapsed,
              sort: LibrarySortMenu(module: 'lidarr', selection: sort,
                onSelected: (field) => ref.read(librarySortProvider('lidarr').notifier).select(field)),
              viewMode: viewMode,
              onViewModeChanged: (value) => ref
                  .read(libraryViewModeProvider('lidarr').notifier).set(value),
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
          children: [
            if (_notifier!.sorting.notice != null)
              ErrorBanner(message: _notifier!.sorting.notice!, maxLines: null,
                onRetry: _notifier!.sorting.canRetry ? _notifier!.sorting.refresh : null),
            if (state.error != null)
              ErrorBanner(
                message: state.error!,
                onRetry: _notifier!.loadArtists,
              ),
            Expanded(
              child: state.isLoading && state.artists.isEmpty
                  ? const Center(
                      child: CircularProgressIndicator(color: AppTheme.accent))
                  : state.error != null && state.artists.isEmpty
                      ? const SizedBox.shrink()
                      : RefreshIndicator(
                          onRefresh: _notifier!.loadArtists,
                          color: AppTheme.accent,
                          child: LidarrArtistList(
                            viewMode: viewMode,
                            scrollKey: 'lidarr-$instanceId-${_notifier!.sorting.effectiveSelection.key}',
                            imageSourceFor: (item) => instanceId == null
                                ? null
                                : lidarrImageSource(ref, item.portraitUrl, instanceId),
                            artists: state.filtered,
                            onTap: _openArtist,
                            onAction: _showActions,
                          ),
                        ),
            ),
          ],
        );
      },
    );
  }
}
