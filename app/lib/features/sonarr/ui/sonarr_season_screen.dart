import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import 'episode_detail_sheet.dart';
import 'sonarr_releases_screen.dart';
import 'sonarr_series_detail_screen.dart' show seasonLabel;
import 'widgets/episode_status.dart';

/// Episode list for one season: number, title, air date and the per-episode
/// status line (download progress / quality+size / Missing / Unaired). Tapping
/// an episode opens its detail sheet; the magnifier runs a per-episode search.
class SonarrSeasonScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final SonarrSeries series;
  final int seasonNumber;

  const SonarrSeasonScreen({
    super.key,
    required this.instanceId,
    required this.series,
    required this.seasonNumber,
  });

  @override
  ConsumerState<SonarrSeasonScreen> createState() =>
      _SonarrSeasonScreenState();
}

class _SonarrSeasonScreenState extends ConsumerState<SonarrSeasonScreen> {
  late final SonarrApiService _service;
  List<SonarrEpisode> _episodes = [];
  Map<int, SonarrQueueItem> _queueByEpisode = {};
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _service = SonarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    setState(() => _isLoading = true);
    try {
      // Kick off both requests, then await — effectively parallel without the
      // heterogeneous Future.wait cast.
      final episodesFuture = _service.getEpisodes(
        widget.series.id,
        seasonNumber: widget.seasonNumber,
        includeEpisodeFile: true,
      );
      final queueFuture = _service.getQueueDetailed();
      final episodes = await episodesFuture;
      final queue = await queueFuture;
      if (!mounted) return;
      episodes.sort((a, b) => b.episodeNumber.compareTo(a.episodeNumber));
      setState(() {
        _episodes = episodes;
        _queueByEpisode = {
          for (final q in queue)
            if (q.episodeId != null) q.episodeId!: q,
        };
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load episodes: $e';
      });
    }
  }

  Future<void> _automaticSeasonSearch() async {
    try {
      await _service.searchSeason(widget.series.id, widget.seasonNumber);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text('Searching for ${seasonLabel(widget.seasonNumber)}…')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Search failed: $e')));
    }
  }

  void _interactiveSeasonSearch() {
    Navigator.of(context, rootNavigator: true).push(
      MaterialPageRoute(
        builder: (_) => SonarrReleasesScreen(
          instanceId: widget.instanceId,
          seriesId: widget.series.id,
          seasonNumber: widget.seasonNumber,
          seriesTitle: widget.series.title,
        ),
      ),
    );
  }

  Future<void> _automaticEpisodeSearch(SonarrEpisode episode) async {
    try {
      await _service.searchEpisodes([episode.id]);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text('Searching for ${episode.seasonEpisodeLabel}…')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Search failed: $e')));
    }
  }

  void _interactiveEpisodeSearch(SonarrEpisode episode) {
    Navigator.of(context, rootNavigator: true).push(
      MaterialPageRoute(
        builder: (_) => SonarrReleasesScreen(
          instanceId: widget.instanceId,
          seriesId: widget.series.id,
          seasonNumber: widget.seasonNumber,
          seriesTitle: widget.series.title,
          episodeId: episode.id,
          episodeLabel: episode.seasonEpisodeLabel,
        ),
      ),
    );
  }

  Future<void> _openEpisode(SonarrEpisode episode) async {
    final changed = await showModalBottomSheet<bool>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (_) => EpisodeDetailSheet(
        instanceId: widget.instanceId,
        series: widget.series,
        episode: episode,
        queueItem: _queueByEpisode[episode.id],
        onAutomaticSearch: () => _automaticEpisodeSearch(episode),
        onInteractiveSearch: () => _interactiveEpisodeSearch(episode),
      ),
    );
    // The episode sheet returns true after an Import Doctor fix.
    if (changed == true && mounted) _load();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppTheme.background,
      appBar: AppBar(
        backgroundColor: AppTheme.background,
        title: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(seasonLabel(widget.seasonNumber)),
            Text(
              widget.series.title,
              style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 12,
                  fontWeight: FontWeight.w400),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
      body: Column(
        children: [
          Expanded(child: _buildBody()),
          _ActionBar(
            onAutomatic: _automaticSeasonSearch,
            onInteractive: _interactiveSeasonSearch,
          ),
        ],
      ),
    );
  }

  Widget _buildBody() {
    if (_isLoading && _episodes.isEmpty) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null && _episodes.isEmpty) {
      return FullScreenError(message: _error!, onRetry: _load);
    }
    if (_episodes.isEmpty) {
      return const Center(
        child: Text('No episodes',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 16)),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: _episodes.length,
        separatorBuilder: (_, __) =>
            const Divider(color: AppTheme.border, height: 1),
        itemBuilder: (context, index) {
          final episode = _episodes[index];
          return _EpisodeTile(
            episode: episode,
            queueItem: _queueByEpisode[episode.id],
            onTap: () => _openEpisode(episode),
            onSearch: () => _interactiveEpisodeSearch(episode),
          );
        },
      ),
    );
  }
}

String formatEpisodeAirDate(SonarrEpisode e) {
  final dt = e.airDateUtc?.toLocal() ??
      (e.airDate != null ? DateTime.tryParse(e.airDate!) : null);
  if (dt == null) return 'TBA';
  return DateFormat('MMMM d, yyyy').format(dt);
}

class _EpisodeTile extends StatelessWidget {
  final SonarrEpisode episode;
  final SonarrQueueItem? queueItem;
  final VoidCallback onTap;
  final VoidCallback onSearch;

  const _EpisodeTile({
    required this.episode,
    required this.queueItem,
    required this.onTap,
    required this.onSearch,
  });

  @override
  Widget build(BuildContext context) {
    final status = episodeStatusLine(episode, queueItem);
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 28,
              child: Text(
                episode.episodeNumber.toString(),
                style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 15,
                    fontWeight: FontWeight.w500),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    episode.title ?? 'Episode ${episode.episodeNumber}',
                    style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 15,
                        fontWeight: FontWeight.w600),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  const SizedBox(height: 2),
                  Text(formatEpisodeAirDate(episode),
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12)),
                  const SizedBox(height: 4),
                  Text(
                    status.text,
                    style: TextStyle(
                        color: status.color,
                        fontSize: 13,
                        fontWeight: FontWeight.w500),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            IconButton(
              icon: const Icon(Icons.search, color: AppTheme.textSecondary),
              tooltip: 'Search releases',
              onPressed: onSearch,
            ),
          ],
        ),
      ),
    );
  }
}

class _ActionBar extends StatelessWidget {
  final VoidCallback onAutomatic;
  final VoidCallback onInteractive;

  const _ActionBar({required this.onAutomatic, required this.onInteractive});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: EdgeInsets.fromLTRB(
          12, 10, 12, 10 + MediaQuery.of(context).padding.bottom),
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        border: Border(top: BorderSide(color: AppTheme.border, width: 0.5)),
      ),
      child: Row(
        children: [
          Expanded(
            child: OutlinedButton.icon(
              onPressed: onAutomatic,
              icon: const Icon(Icons.search, size: 18, color: AppTheme.available),
              label: const Text('Automatic',
                  style: TextStyle(color: AppTheme.textPrimary)),
              style: OutlinedButton.styleFrom(
                side: const BorderSide(color: AppTheme.border),
                shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(10)),
                padding: const EdgeInsets.symmetric(vertical: 12),
              ),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: OutlinedButton.icon(
              onPressed: onInteractive,
              icon: const Icon(Icons.person_outline,
                  size: 18, color: AppTheme.available),
              label: const Text('Interactive',
                  style: TextStyle(color: AppTheme.textPrimary)),
              style: OutlinedButton.styleFrom(
                side: const BorderSide(color: AppTheme.border),
                shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(10)),
                padding: const EdgeInsets.symmetric(vertical: 12),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
