import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/day_sections.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import 'sonarr_series_detail_screen.dart';

/// Sonarr calendar: recent and upcoming episodes grouped by air day, each row
/// carrying the series poster and opening the series detail screen.
class SonarrCalendarScreen extends ConsumerStatefulWidget {
  const SonarrCalendarScreen({super.key});

  @override
  ConsumerState<SonarrCalendarScreen> createState() =>
      _SonarrCalendarScreenState();
}

class _SonarrCalendarScreenState extends ConsumerState<SonarrCalendarScreen> {
  List<SonarrCalendarEntry> _entries = [];
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
    final instanceId = ref.read(instanceProvider).activeSonarrInstance?.id;
    if (instanceId == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Sonarr instance configured';
      });
      return;
    }

    final gen = ++_loadGeneration;
    setState(() => _isLoading = true);
    try {
      final service = SonarrApiService(
        backendDio: ref.read(backendClientProvider),
        instanceId: instanceId,
      );
      final now = DateTime.now();
      final events = await service.getCalendar(
        start: now.subtract(const Duration(days: 7)).toIso8601String(),
        end: now.add(const Duration(days: 14)).toIso8601String(),
        includeSeries: true,
      );
      if (!mounted || gen != _loadGeneration) return;
      setState(() {
        _entries = events
            .map((e) => SonarrCalendarEntry.fromJson(e))
            .toList();
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

  Future<void> _openSeries(SonarrCalendarEntry entry) async {
    final series = entry.series;
    final instanceId = ref.read(instanceProvider).activeSonarrInstance?.id;
    if (series == null || instanceId == null) return;
    await Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) =>
            SonarrSeriesDetailScreen(instanceId: instanceId, series: series),
      ),
    );
    // The detail screen can edit or remove the series; refresh on return.
    _loadCalendar();
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(instanceProvider.select((s) => s.activeSonarrInstanceId),
        (_, __) => _loadCalendar());

    if (_isLoading) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null) {
      return FullScreenError(message: _error!, onRetry: _loadCalendar);
    }
    if (_entries.isEmpty) {
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
              child: Text('No upcoming episodes',
                  style: TextStyle(
                      color: AppTheme.textSecondary, fontSize: 16)),
            ),
          ],
        ),
      );
    }

    final groups = groupItemsByDay(
      _entries.where((e) => e.airDateUtc != null),
      (e) => e.airDateUtc!.toLocal(),
    );
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
          final entry = item as SonarrCalendarEntry;
          return _CalendarTile(
            entry: entry,
            onTap: entry.series != null ? () => _openSeries(entry) : null,
          );
        },
      ),
    );
  }
}

class _CalendarTile extends StatelessWidget {
  final SonarrCalendarEntry entry;
  final VoidCallback? onTap;

  const _CalendarTile({required this.entry, this.onTap});

  @override
  Widget build(BuildContext context) {
    final episodeTitle = entry.title;
    final subtitle = (episodeTitle != null && episodeTitle.isNotEmpty)
        ? '${entry.seasonEpisodeLabel} • $episodeTitle'
        : entry.seasonEpisodeLabel;
    final air = entry.airDateUtc?.toLocal();

    return ListTile(
      onTap: onTap,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 2),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 45,
          height: 67,
          child: CachedImage(
            url: entry.series?.posterUrl,
            fit: BoxFit.cover,
            icon: Icons.tv,
          ),
        ),
      ),
      title: Text(
        entry.series?.title ?? 'Unknown series',
        style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 13.5,
            fontWeight: FontWeight.w500),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Text(
        subtitle,
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: Column(
        mainAxisSize: MainAxisSize.min,
        mainAxisAlignment: MainAxisAlignment.center,
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          if (air != null)
            Text(
              DateFormat('h:mm a').format(air),
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 12),
            ),
          if (entry.hasFile)
            Padding(
              padding: EdgeInsets.only(top: air != null ? 4 : 0),
              child: const Icon(Icons.check_circle,
                  size: 16, color: AppTheme.available),
            ),
        ],
      ),
    );
  }
}
