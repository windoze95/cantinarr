import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/day_sections.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_calendar.dart';
import '../data/radarr_models.dart';
import 'radarr_movie_detail_screen.dart';

/// Radarr calendar: movie releases grouped by day, one labelled row per
/// release type (cinema / digital / physical), each opening the movie detail
/// screen.
class RadarrCalendarScreen extends ConsumerStatefulWidget {
  const RadarrCalendarScreen({super.key});

  @override
  ConsumerState<RadarrCalendarScreen> createState() =>
      _RadarrCalendarScreenState();
}

class _RadarrCalendarScreenState extends ConsumerState<RadarrCalendarScreen> {
  List<RadarrCalendarRelease> _releases = [];
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
    final instanceId = ref.read(instanceProvider).activeRadarrInstance?.id;
    if (instanceId == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Radarr instance configured';
      });
      return;
    }

    final gen = ++_loadGeneration;
    setState(() => _isLoading = true);
    try {
      final service = RadarrApiService(
        backendDio: ref.read(backendClientProvider),
        instanceId: instanceId,
      );
      final now = DateTime.now();
      final start = now.subtract(const Duration(days: 7));
      final end = now.add(const Duration(days: 30));
      final events = await service.getCalendar(
        start: start.toIso8601String(),
        end: end.toIso8601String(),
      );
      if (!mounted || gen != _loadGeneration) return;
      setState(() {
        _releases = radarrCalendarReleases(
          events.map((e) => RadarrMovie.fromJson(e)),
          start: start,
          end: end,
        );
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

  Future<void> _openMovie(RadarrMovie movie) async {
    final instanceId = ref.read(instanceProvider).activeRadarrInstance?.id;
    if (instanceId == null) return;
    await Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) =>
            RadarrMovieDetailScreen(instanceId: instanceId, movie: movie),
      ),
    );
    // The detail screen can edit or remove the movie; refresh on return.
    _loadCalendar();
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(instanceProvider.select((s) => s.activeRadarrInstanceId),
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
          final release = item as RadarrCalendarRelease;
          return _CalendarTile(
            release: release,
            onTap: () => _openMovie(release.movie),
          );
        },
      ),
    );
  }
}

class _CalendarTile extends StatelessWidget {
  final RadarrCalendarRelease release;
  final VoidCallback? onTap;

  const _CalendarTile({required this.release, this.onTap});

  @override
  Widget build(BuildContext context) {
    final movie = release.movie;

    return ListTile(
      onTap: onTap,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 2),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 45,
          height: 67,
          child: CachedImage(
            url: movie.posterUrl,
            fit: BoxFit.cover,
            icon: Icons.movie,
          ),
        ),
      ),
      title: Text(
        movie.year > 0 ? '${movie.title} (${movie.year})' : movie.title,
        style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 13.5,
            fontWeight: FontWeight.w500),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        release.label,
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: movie.hasFile
          ? const Icon(Icons.check_circle, size: 16, color: AppTheme.available)
          : null,
    );
  }
}
