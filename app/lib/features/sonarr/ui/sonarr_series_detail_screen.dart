import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/action_sheet.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import 'edit_series_screen.dart';
import 'series_actions.dart';
import 'sonarr_releases_screen.dart';
import 'sonarr_season_screen.dart';

/// Series detail: an "All Seasons" summary plus a per-season list with
/// availability and monitor toggles. Tapping a season drills into its
/// episodes; long-pressing one offers Automatic/Interactive season search.
/// The app bar carries external links, Edit Series, and the series action
/// menu. Mirrors LunaSea's Series Details > Seasons view.
class SonarrSeriesDetailScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final SonarrSeries series;

  const SonarrSeriesDetailScreen({
    super.key,
    required this.instanceId,
    required this.series,
  });

  @override
  ConsumerState<SonarrSeriesDetailScreen> createState() =>
      _SonarrSeriesDetailScreenState();
}

class _SonarrSeriesDetailScreenState
    extends ConsumerState<SonarrSeriesDetailScreen> {
  late final SonarrApiService _service;
  late SonarrSeries _series;
  bool _isLoading = true;
  String? _error;
  final Set<int> _togglingSeasons = {};

  @override
  void initState() {
    super.initState();
    _series = widget.series;
    _service = SonarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    setState(() => _isLoading = true);
    try {
      final series = await _service.getSeriesById(_series.id);
      if (!mounted) return;
      setState(() {
        _series = series;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load series: $e';
      });
    }
  }

  Future<void> _toggleSeasonMonitored(SonarrSeason season) async {
    final target = !season.monitored;
    setState(() => _togglingSeasons.add(season.seasonNumber));
    try {
      await _service.setSeasonMonitored(
        _series.id,
        season.seasonNumber,
        monitored: target,
      );
      if (!mounted) return;
      // Reflect the change locally without a full reload.
      final seasons = _series.seasons
          .map((s) => s.seasonNumber == season.seasonNumber
              ? SonarrSeason(
                  seasonNumber: s.seasonNumber,
                  monitored: target,
                  statistics: s.statistics,
                )
              : s)
          .toList();
      setState(() {
        _series = SonarrSeries(
          id: _series.id,
          title: _series.title,
          tvdbId: _series.tvdbId,
          tmdbId: _series.tmdbId,
          year: _series.year,
          overview: _series.overview,
          titleSlug: _series.titleSlug,
          monitored: _series.monitored,
          path: _series.path,
          seriesType: _series.seriesType,
          images: _series.images,
          statistics: _series.statistics,
          status: _series.status,
          qualityProfileId: _series.qualityProfileId,
          seasons: seasons,
        );
      });
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Could not change monitoring: $e')));
    } finally {
      if (mounted) setState(() => _togglingSeasons.remove(season.seasonNumber));
    }
  }

  void _openSeason(SonarrSeason season) {
    Navigator.of(context, rootNavigator: true).push(
      MaterialPageRoute(
        builder: (_) => SonarrSeasonScreen(
          instanceId: widget.instanceId,
          series: _series,
          seasonNumber: season.seasonNumber,
        ),
      ),
    );
  }

  void _toast(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  /// Long-press menu for a season: automatic or interactive season search.
  Future<void> _showSeasonActions(SonarrSeason season) async {
    final action = await showActionSheet<String>(
      context,
      title: seasonLabel(season.seasonNumber),
      actions: const [
        SheetAction('automatic', Icons.search, 'Automatic Search'),
        SheetAction('interactive', Icons.manage_search, 'Interactive Search'),
      ],
    );
    if (action == null || !mounted) return;
    switch (action) {
      case 'automatic':
        try {
          await _service.searchSeason(_series.id, season.seasonNumber);
          _toast('Searching for ${seasonLabel(season.seasonNumber)}…');
        } catch (e) {
          _toast('Search failed: $e');
        }
      case 'interactive':
        Navigator.of(context, rootNavigator: true).push(
          MaterialPageRoute(
            builder: (_) => SonarrReleasesScreen(
              instanceId: widget.instanceId,
              seriesId: _series.id,
              seasonNumber: season.seasonNumber,
              seriesTitle: _series.title,
            ),
          ),
        );
    }
  }

  Future<void> _openEdit() async {
    final saved = await Navigator.of(context, rootNavigator: true).push<bool>(
      MaterialPageRoute(
        builder: (_) => EditSeriesScreen(
          instanceId: widget.instanceId,
          series: _series,
        ),
      ),
    );
    if (saved == true && mounted) _load();
  }

  void _showSeriesMenu() {
    showSeriesActions(
      context,
      service: _service,
      instanceId: widget.instanceId,
      series: _series,
      onChanged: _load,
      onRemoved: () {
        if (mounted) Navigator.of(context).pop();
      },
    );
  }

  /// External sites for this series; entries appear only when the matching id
  /// is known.
  Future<void> _openLinks() async {
    final links = <SheetAction<String>>[
      if ((_series.imdbId ?? '').isNotEmpty)
        SheetAction('https://www.imdb.com/title/${_series.imdbId}',
            Icons.movie_outlined, 'IMDb'),
      if ((_series.tvdbId ?? 0) > 0)
        SheetAction('https://thetvdb.com/?tab=series&id=${_series.tvdbId}',
            Icons.live_tv_outlined, 'TheTVDB'),
      if ((_series.tmdbId ?? 0) > 0)
        SheetAction('https://www.themoviedb.org/tv/${_series.tmdbId}',
            Icons.theaters_outlined, 'TMDB'),
      if ((_series.tvdbId ?? 0) > 0)
        SheetAction(
            'https://trakt.tv/search/tvdb/${_series.tvdbId}?id_type=show',
            Icons.track_changes_outlined,
            'Trakt'),
    ];
    if (links.isEmpty) {
      _toast('No external links available');
      return;
    }
    final url =
        await showActionSheet<String>(context, title: _series.title, actions: links);
    if (url == null) return;
    launchUrl(Uri.parse(url), mode: LaunchMode.externalApplication);
  }

  @override
  Widget build(BuildContext context) {
    final seasons = [..._series.seasons]
      ..sort((a, b) => b.seasonNumber.compareTo(a.seasonNumber));

    return Scaffold(
      backgroundColor: AppTheme.background,
      appBar: AppBar(
        backgroundColor: AppTheme.background,
        title: Text(_series.title, maxLines: 1, overflow: TextOverflow.ellipsis),
        actions: [
          IconButton(
            icon: const Icon(Icons.link, color: AppTheme.textPrimary),
            tooltip: 'External links',
            onPressed: _openLinks,
          ),
          IconButton(
            icon: const Icon(Icons.edit_outlined, color: AppTheme.textPrimary),
            tooltip: 'Edit series',
            onPressed: _openEdit,
          ),
          IconButton(
            icon: const Icon(Icons.more_vert, color: AppTheme.textPrimary),
            tooltip: 'Series actions',
            onPressed: _showSeriesMenu,
          ),
        ],
      ),
      body: _error != null && _series.seasons.isEmpty
          ? FullScreenError(message: _error!, onRetry: _load)
          : RefreshIndicator(
              onRefresh: _load,
              color: AppTheme.accent,
              child: ListView(
                padding: const EdgeInsets.symmetric(vertical: 12),
                children: [
                  if (_error != null)
                    ErrorBanner(message: _error!, onRetry: _load),
                  _AllSeasonsCard(series: _series),
                  const SizedBox(height: 4),
                  ...seasons.map((s) => _SeasonCard(
                        season: s,
                        busy: _togglingSeasons.contains(s.seasonNumber),
                        onTap: () => _openSeason(s),
                        onLongPress: () => _showSeasonActions(s),
                        onToggleMonitored: () => _toggleSeasonMonitored(s),
                      )),
                  if (seasons.isEmpty && !_isLoading)
                    const Padding(
                      padding: EdgeInsets.all(32),
                      child: Center(
                        child: Text('No seasons',
                            style: TextStyle(color: AppTheme.textSecondary)),
                      ),
                    ),
                ],
              ),
            ),
    );
  }
}

String seasonLabel(int seasonNumber) =>
    seasonNumber == 0 ? 'Specials' : 'Season $seasonNumber';

/// "X/Y Episodes Available" with a colour: green at 100%, amber/red otherwise.
class _AvailabilityLine extends StatelessWidget {
  final SonarrStatistics? stats;
  const _AvailabilityLine({required this.stats});

  @override
  Widget build(BuildContext context) {
    final s = stats;
    if (s == null || s.episodeCount == 0) {
      return const Text('0% • 0/0 Episodes Available',
          style: TextStyle(color: AppTheme.textSecondary, fontSize: 13));
    }
    final pct = (s.episodeFileCount / s.episodeCount * 100).round();
    final complete = s.episodeFileCount >= s.episodeCount;
    return Text(
      '$pct% • ${s.episodeFileCount}/${s.episodeCount} Episodes Available',
      style: TextStyle(
        color: complete ? AppTheme.available : AppTheme.error,
        fontSize: 13,
        fontWeight: FontWeight.w500,
      ),
    );
  }
}

class _AllSeasonsCard extends StatelessWidget {
  final SonarrSeries series;
  const _AllSeasonsCard({required this.series});

  @override
  Widget build(BuildContext context) {
    final stats = series.statistics;
    return Container(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border, width: 0.5),
      ),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text('All Seasons',
                    style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 17,
                        fontWeight: FontWeight.bold)),
                if (stats != null && stats.sizeOnDisk > 0) ...[
                  const SizedBox(height: 4),
                  Text(stats.sizeFormatted,
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 13)),
                ],
                const SizedBox(height: 6),
                _AvailabilityLine(stats: stats),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _SeasonCard extends StatelessWidget {
  final SonarrSeason season;
  final bool busy;
  final VoidCallback onTap;
  final VoidCallback onLongPress;
  final VoidCallback onToggleMonitored;

  const _SeasonCard({
    required this.season,
    required this.busy,
    required this.onTap,
    required this.onLongPress,
    required this.onToggleMonitored,
  });

  @override
  Widget build(BuildContext context) {
    final stats = season.statistics;
    return InkWell(
      onTap: onTap,
      onLongPress: onLongPress,
      child: Container(
        margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        padding: const EdgeInsets.all(14),
        decoration: BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(color: AppTheme.border, width: 0.5),
        ),
        child: Row(
          children: [
            Container(
              width: 44,
              height: 60,
              decoration: BoxDecoration(
                color: AppTheme.surfaceVariant,
                borderRadius: BorderRadius.circular(6),
              ),
              child: Icon(
                Icons.video_library_outlined,
                color: season.monitored ? AppTheme.available : AppTheme.unavailable,
                size: 22,
              ),
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(seasonLabel(season.seasonNumber),
                      style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 16,
                          fontWeight: FontWeight.w600)),
                  if (stats != null && stats.sizeOnDisk > 0) ...[
                    const SizedBox(height: 4),
                    Text(stats.sizeFormatted,
                        style: const TextStyle(
                            color: AppTheme.textSecondary, fontSize: 13)),
                  ],
                  const SizedBox(height: 6),
                  _AvailabilityLine(stats: stats),
                ],
              ),
            ),
            IconButton(
              onPressed: busy ? null : onToggleMonitored,
              tooltip: season.monitored ? 'Stop monitoring' : 'Monitor',
              icon: busy
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(
                          strokeWidth: 2, color: AppTheme.accent))
                  : Icon(
                      season.monitored
                          ? Icons.bookmark
                          : Icons.bookmark_border,
                      color: season.monitored
                          ? AppTheme.accent
                          : AppTheme.textSecondary,
                    ),
            ),
          ],
        ),
      ),
    );
  }
}
