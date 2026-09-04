import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/day_sections.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/lidarr_api_service.dart';
import '../data/lidarr_calendar.dart';
import '../data/lidarr_image.dart';
import '../data/lidarr_models.dart';
import 'lidarr_artist_screen.dart';

/// Lidarr calendar: album releases grouped by day. Album release dates are
/// calendar dates (one per album, no time-of-day), so this is the Radarr
/// screen's shape without the per-type expansion. Tapping a row opens the
/// artist's page, where the album and its request/search controls live.
class LidarrCalendarScreen extends ConsumerStatefulWidget {
  const LidarrCalendarScreen({super.key});

  @override
  ConsumerState<LidarrCalendarScreen> createState() =>
      _LidarrCalendarScreenState();
}

class _LidarrCalendarScreenState extends ConsumerState<LidarrCalendarScreen> {
  List<LidarrCalendarRelease> _releases = [];
  bool _isLoading = true;

  /// Bumped by every fresh load (instance switch, refresh) so in-flight
  /// responses from a superseded fetch are dropped.
  int _loadGeneration = 0;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _loadCalendar());
  }

  Future<void> _loadCalendar() async {
    final instanceId = ref.read(instanceProvider).activeLidarrInstanceId;
    if (instanceId == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Lidarr instance configured';
      });
      return;
    }

    final gen = ++_loadGeneration;
    setState(() => _isLoading = true);
    try {
      final service = LidarrApiService(
        backendDio: ref.read(backendClientProvider),
        instanceId: instanceId,
      );
      final now = DateTime.now();
      final start = now.subtract(const Duration(days: 7));
      final end = now.add(const Duration(days: 30));
      final albums = await service.getCalendar(
        start: start.toIso8601String(),
        end: end.toIso8601String(),
      );
      if (!mounted || gen != _loadGeneration) return;
      setState(() {
        _releases = lidarrCalendarReleases(albums, start: start, end: end);
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted || gen != _loadGeneration) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load calendar: $e';
      });
    }
  }

  Future<void> _openArtist(LidarrAlbum album) async {
    final instanceId = ref.read(instanceProvider).activeLidarrInstanceId;
    if (instanceId == null) return;
    await Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => LidarrArtistScreen(
          instanceId: instanceId,
          artistId: album.artistId,
          artistName: album.artistName.isEmpty ? null : album.artistName,
        ),
      ),
    );
    // The artist page can change monitoring or trigger searches; refresh.
    _loadCalendar();
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(instanceProvider.select((s) => s.activeLidarrInstanceId),
        (_, __) => _loadCalendar());

    if (_isLoading) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null) {
      return FullScreenError(message: _error!, onRetry: _loadCalendar);
    }
    if (_releases.isEmpty) {
      return RefreshIndicator(
        onRefresh: _loadCalendar,
        color: AppTheme.accent,
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          children: const [
            SizedBox(height: 160),
            Icon(Icons.calendar_today_outlined,
                size: 48, color: AppTheme.textSecondary),
            SizedBox(height: 12),
            Center(
              child: Text('No upcoming releases',
                  style: TextStyle(
                      color: AppTheme.textSecondary, fontSize: 16)),
            ),
          ],
        ),
      );
    }

    final groups = groupItemsByDay(_releases, (r) => r.date);
    // Flatten day groups into a single list of headers + tiles.
    final items = <Object>[];
    for (final group in groups) {
      items.add(group.key);
      items.addAll(group.value);
    }
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);

    return RefreshIndicator(
      onRefresh: _loadCalendar,
      color: AppTheme.accent,
      child: ListView.builder(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.only(top: 4, bottom: 24),
        itemCount: items.length,
        itemBuilder: (context, index) {
          final item = items[index];
          if (item is DateTime) {
            return DaySectionHeader(day: item, today: today);
          }
          final release = item as LidarrCalendarRelease;
          return _CalendarTile(
            release: release,
            onTap: () => _openArtist(release.album),
          );
        },
      ),
    );
  }
}

class _CalendarTile extends ConsumerWidget {
  final LidarrCalendarRelease release;
  final VoidCallback? onTap;

  const _CalendarTile({required this.release, this.onTap});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final album = release.album;
    final instanceId = ref.read(instanceProvider).activeLidarrInstanceId;
    final cover = instanceId == null
        ? null
        : lidarrImageSource(ref, album.coverUrl, instanceId);

    return ListTile(
      onTap: onTap,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 2),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 48,
          height: 48,
          child: CachedImage(
            url: cover?.url,
            headers: cover?.headers,
            fit: BoxFit.cover,
            icon: Icons.album,
          ),
        ),
      ),
      title: Text(
        album.year > 0 ? '${album.title} (${album.year})' : album.title,
        style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 13.5,
            fontWeight: FontWeight.w500),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        [
          if (album.artistName.isNotEmpty) album.artistName,
          if (album.albumType != null && album.albumType!.isNotEmpty)
            album.albumType!,
        ].join(' • '),
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: album.hasFiles
          ? const Icon(Icons.check_circle, size: 16, color: AppTheme.available)
          : null,
    );
  }
}
