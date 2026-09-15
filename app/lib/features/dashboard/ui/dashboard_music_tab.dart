import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../discover/logic/music_browse_query.dart';
import '../../discover/logic/discovery_access.dart';
import '../../discover/ui/music_discovery_row.dart';
import '../data/music_artists_service.dart';
import '../data/music_library_service.dart';
import '../data/recent_albums_service.dart';
import 'library_artists_row.dart';
import 'recently_added_albums_row.dart';

/// Dashboard Music tab: public discovery followed by live library rows.
/// Lidarr album/artist search lives in the shell toolbar
/// (`shellMusicSearchProvider` / `MusicSearchResultsView`) — the shell overlay
/// covers this tab's body while a search is active, so these rows never hide
/// themselves during one.
class DashboardMusicTab extends ConsumerStatefulWidget {
  const DashboardMusicTab({super.key});

  @override
  ConsumerState<DashboardMusicTab> createState() => _DashboardMusicTabState();
}

class _DashboardMusicTabState extends ConsumerState<DashboardMusicTab>
    with WidgetsBindingObserver {
  String _period = 'this_week';
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // The library may have changed while the app was backgrounded (downloads
    // finishing, an admin working directly in Lidarr) — re-pull the browse
    // rows' truth.
    if (state == AppLifecycleState.resumed) _refreshMusicTruth();
  }

  void _refreshMusicTruth() {
    final instanceId = ref.read(instanceProvider).activeLidarrInstance?.id;
    ref.invalidate(ownedAlbumsForInstanceProvider(instanceId));
    ref.invalidate(ownedAlbumsProvider);
    ref.invalidate(recentAlbumsForInstanceProvider(instanceId));
    ref.invalidate(recentAlbumsProvider);
    ref.invalidate(musicArtistsProvider);
    ref.read(libraryRefreshTickProvider.notifier).state++;
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.hasValue) _refreshMusicTruth();
    });
    ref.listen(
      instanceProvider.select((state) => state.activeLidarrInstance?.id),
      (previous, next) {
        if (previous == next) return;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (!mounted) return;
          ref.invalidate(ownedAlbumsProvider);
        });
      },
    );
    final access = ref.watch(discoveryAccessProvider);
    final instanceId = access.activeId('lidarr');
    return SingleChildScrollView(
      key: const PageStorageKey('dashboard-music'),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (access.needsUpdate(instanceId))
            const Padding(
                padding: EdgeInsets.all(16),
                child: Text(adminCatalogUpdateMessage)),
          if (access.canBrowse('lidarr', instanceId)) ...[
            MusicDiscoveryRow(
              query: MusicBrowseQuery(
                  feed: 'popular', instanceId: instanceId, period: _period),
              onPeriodChanged: (period) => setState(() => _period = period),
            ),
            MusicDiscoveryRow(
                query: MusicBrowseQuery(
                    feed: 'new-releases', instanceId: instanceId)),
            MusicGenreStrip(instanceId: instanceId),
          ],
          if (instanceId != null) ...const [
            RecentlyAddedAlbumsRow(),
            LibraryArtistsRow(),
          ],
        ],
      ),
    );
  }
}
