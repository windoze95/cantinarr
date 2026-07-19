import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/action_sheet.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../navigation/ambient_page_route.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import '../logic/episode_selection.dart';
import 'episode_detail_sheet.dart';
import 'sonarr_releases_screen.dart';
import 'sonarr_series_detail_screen.dart' show seasonLabel;
import 'widgets/episode_status.dart';

/// Episode list for one season — or the whole series when [seasonNumber] is
/// null ("All Seasons", grouped by season headers). Tapping an episode opens
/// its detail sheet; the magnifier runs an automatic per-episode search;
/// long-pressing one opens its action menu (searches, select, monitor toggle,
/// delete file). The Automatic button's arrow enters selection mode — pick
/// episodes with All / Undownloaded / None quick-selects, then search them all
/// at once or delete their downloaded files.
class SonarrSeasonScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final SonarrSeries series;

  /// The season to show, or null for every season in the series.
  final int? seasonNumber;

  const SonarrSeasonScreen({
    super.key,
    required this.instanceId,
    required this.series,
    this.seasonNumber,
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
  bool _selecting = false;
  final Set<int> _selectedIds = {};

  bool get _allSeasons => widget.seasonNumber == null;

  String get _title =>
      _allSeasons ? 'All Seasons' : seasonLabel(widget.seasonNumber!);

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
      episodes.sort((a, b) => b.seasonNumber != a.seasonNumber
          ? b.seasonNumber.compareTo(a.seasonNumber)
          : b.episodeNumber.compareTo(a.episodeNumber));
      setState(() {
        _episodes = episodes;
        _queueByEpisode = {
          for (final q in queue)
            if (q.episodeId != null) q.episodeId!: q,
        };
        // Drop selected ids that no longer exist after a refresh.
        final ids = {for (final e in episodes) e.id};
        _selectedIds.retainAll(ids);
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

  void _toast(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
  }

  /// Season scope: SeasonSearch for one season, SeriesSearch (monitored
  /// episodes) for All Seasons.
  Future<void> _automaticSearch() async {
    try {
      if (_allSeasons) {
        await _service.searchSeries(widget.series.id);
        _toast('Searching for monitored episodes of ${widget.series.title}…');
      } else {
        await _service.searchSeason(widget.series.id, widget.seasonNumber!);
        _toast('Searching for ${seasonLabel(widget.seasonNumber!)}…');
      }
    } catch (e) {
      _toast('Search failed: $e');
    }
  }

  /// Interactive search is season-scoped; in All Seasons mode pick the season
  /// first.
  Future<void> _interactiveSearch() async {
    var seasonNumber = widget.seasonNumber;
    if (seasonNumber == null) {
      final seasons = [...widget.series.seasons]
        ..sort((a, b) => a.seasonNumber.compareTo(b.seasonNumber));
      if (seasons.isEmpty) {
        _toast('No seasons available');
        return;
      }
      seasonNumber = await showDialog<int>(
        context: context,
        builder: (ctx) => SimpleDialog(
          backgroundColor: AppTheme.surface,
          title: const Text('Select Season'),
          children: seasons
              .map((s) => SimpleDialogOption(
                    onPressed: () => Navigator.pop(ctx, s.seasonNumber),
                    child: Text(
                      seasonLabel(s.seasonNumber),
                      style: TextStyle(
                        color: s.monitored
                            ? AppTheme.textPrimary
                            : AppTheme.textSecondary,
                        fontSize: 15,
                      ),
                    ),
                  ))
              .toList(),
        ),
      );
      if (seasonNumber == null || !mounted) return;
    }
    Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => SonarrReleasesScreen(
          instanceId: widget.instanceId,
          seriesId: widget.series.id,
          seasonNumber: seasonNumber!,
          seriesTitle: widget.series.title,
        ),
      ),
    );
  }

  Future<void> _automaticEpisodeSearch(SonarrEpisode episode) async {
    try {
      await _service.searchEpisodes([episode.id]);
      _toast('Searching for ${episode.seasonEpisodeLabel}…');
    } catch (e) {
      _toast('Search failed: $e');
    }
  }

  void _interactiveEpisodeSearch(SonarrEpisode episode) {
    Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => SonarrReleasesScreen(
          instanceId: widget.instanceId,
          seriesId: widget.series.id,
          seasonNumber: episode.seasonNumber,
          seriesTitle: widget.series.title,
          episodeId: episode.id,
          episodeLabel: episode.seasonEpisodeLabel,
        ),
      ),
    );
  }

  /// Long-press menu for one episode.
  Future<void> _showEpisodeMenu(SonarrEpisode episode) async {
    final title = episode.title != null && episode.title!.isNotEmpty
        ? '${episode.seasonEpisodeLabel} • ${episode.title}'
        : episode.seasonEpisodeLabel;
    final action = await showActionSheet<String>(
      context,
      title: title,
      actions: [
        const SheetAction('automatic', Icons.search, 'Automatic Search'),
        const SheetAction(
            'interactive', Icons.manage_search, 'Interactive Search'),
        const SheetAction('select', Icons.checklist, 'Select Episodes'),
        SheetAction(
            'monitor',
            episode.monitored ? Icons.bookmark_border : Icons.bookmark,
            episode.monitored ? 'Unmonitor Episode' : 'Monitor Episode'),
        if (episode.hasFile)
          const SheetAction('delete', Icons.delete_outline, 'Delete File',
              color: AppTheme.error),
      ],
    );
    if (action == null || !mounted) return;
    switch (action) {
      case 'automatic':
        _automaticEpisodeSearch(episode);
      case 'interactive':
        _interactiveEpisodeSearch(episode);
      case 'select':
        _startSelecting(preselect: [episode.id]);
      case 'monitor':
        try {
          await _service.setEpisodesMonitored([episode.id],
              monitored: !episode.monitored);
          _toast(episode.monitored
              ? 'Unmonitored ${episode.seasonEpisodeLabel}'
              : 'Monitoring ${episode.seasonEpisodeLabel}');
          _load();
        } catch (e) {
          _toast('Could not change monitoring: $e');
        }
      case 'delete':
        _confirmDeleteFiles([episode]);
    }
  }

  /// Confirms then deletes the downloaded files of [episodes] (those that
  /// have one). Used by the episode menu and the selection-mode Delete button.
  Future<void> _confirmDeleteFiles(List<SonarrEpisode> episodes) async {
    final withFiles = [
      for (final e in episodes)
        if (e.hasFile && e.episodeFileId > 0) e,
    ];
    if (withFiles.isEmpty) return;
    final what = withFiles.length == 1
        ? 'the downloaded file for ${withFiles.single.seasonEpisodeLabel}'
        : '${withFiles.length} downloaded files';
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Delete Files'),
        content: Text('Delete $what from disk?'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('Cancel')),
          TextButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: TextButton.styleFrom(foregroundColor: AppTheme.error),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await _service
          .deleteEpisodeFiles([for (final e in withFiles) e.episodeFileId]);
      _toast(withFiles.length == 1
          ? 'Deleted 1 file'
          : 'Deleted ${withFiles.length} files');
      if (_selecting) _stopSelecting();
      _load();
    } catch (e) {
      _toast('Delete failed: $e');
    }
  }

  // --- Episode selection mode ---

  void _startSelecting({Iterable<int> preselect = const []}) {
    setState(() {
      _selecting = true;
      _selectedIds
        ..clear()
        ..addAll(preselect);
    });
  }

  void _stopSelecting() {
    setState(() {
      _selecting = false;
      _selectedIds.clear();
    });
  }

  void _toggleSelected(SonarrEpisode episode) {
    setState(() {
      if (!_selectedIds.remove(episode.id)) _selectedIds.add(episode.id);
    });
  }

  /// Entry point for the Automatic button's "Individual episodes" option:
  /// everything aired-but-missing starts selected.
  void _startIndividualDownloads() {
    _startSelecting(preselect: undownloadedEpisodeIds(_episodes));
  }

  List<SonarrEpisode> get _selectedEpisodes =>
      [for (final e in _episodes) if (_selectedIds.contains(e.id)) e];

  /// Selected episodes that actually have a file on disk (deletable).
  int get _selectedFileCount =>
      _selectedEpisodes.where((e) => e.hasFile && e.episodeFileId > 0).length;

  Future<void> _searchSelected() async {
    final ids = _selectedIds.toList()..sort();
    if (ids.isEmpty) return;
    try {
      await _service.searchEpisodes(ids);
      _toast(ids.length == 1
          ? 'Searching for 1 episode…'
          : 'Searching for ${ids.length} episodes…');
      _stopSelecting();
    } catch (e) {
      _toast('Search failed: $e');
    }
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
            Text(_title),
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
          if (_selecting)
            _SelectionBar(
              selectedCount: _selectedIds.length,
              onSelectAll: () => setState(() {
                _selectedIds
                  ..clear()
                  ..addAll(_episodes.map((e) => e.id));
              }),
              onSelectUndownloaded: () => setState(() {
                _selectedIds
                  ..clear()
                  ..addAll(undownloadedEpisodeIds(_episodes));
              }),
              onSelectNone: () => setState(_selectedIds.clear),
              onClose: _stopSelecting,
            ),
          Expanded(child: _buildBody()),
          _ActionBar(
            selecting: _selecting,
            selectedCount: _selectedIds.length,
            selectedFileCount: _selectedFileCount,
            onAutomatic: _automaticSearch,
            onAutomaticEpisodes: _startIndividualDownloads,
            onInteractive: _interactiveSearch,
            onSearchSelected: _searchSelected,
            onDeleteSelected: () => _confirmDeleteFiles(_selectedEpisodes),
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

    // In All Seasons mode a header row precedes each season's episodes.
    final rows = <Object>[];
    int? lastSeason;
    for (final e in _episodes) {
      if (_allSeasons && e.seasonNumber != lastSeason) {
        rows.add(e.seasonNumber);
        lastSeason = e.seasonNumber;
      }
      rows.add(e);
    }

    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: rows.length,
        separatorBuilder: (_, index) =>
            rows[index] is int || rows[index + 1] is int
                ? const SizedBox.shrink()
                : const Divider(color: AppTheme.border, height: 1),
        itemBuilder: (context, index) {
          final row = rows[index];
          if (row is int) return _SeasonHeader(seasonNumber: row);
          final episode = row as SonarrEpisode;
          return _EpisodeTile(
            episode: episode,
            queueItem: _queueByEpisode[episode.id],
            selecting: _selecting,
            selected: _selectedIds.contains(episode.id),
            onTap: _selecting
                ? () => _toggleSelected(episode)
                : () => _openEpisode(episode),
            onLongPress: _selecting ? null : () => _showEpisodeMenu(episode),
            onSearch: () => _automaticEpisodeSearch(episode),
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

/// "Season N" divider used in All Seasons mode.
class _SeasonHeader extends StatelessWidget {
  final int seasonNumber;
  const _SeasonHeader({required this.seasonNumber});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 18, 16, 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(seasonLabel(seasonNumber),
              style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 16,
                  fontWeight: FontWeight.bold)),
          const SizedBox(height: 4),
          Container(width: 44, height: 2, color: AppTheme.accent),
        ],
      ),
    );
  }
}

class _EpisodeTile extends StatelessWidget {
  final SonarrEpisode episode;
  final SonarrQueueItem? queueItem;
  final bool selecting;
  final bool selected;
  final VoidCallback onTap;
  final VoidCallback? onLongPress;
  final VoidCallback onSearch;

  const _EpisodeTile({
    required this.episode,
    required this.queueItem,
    required this.selecting,
    required this.selected,
    required this.onTap,
    required this.onLongPress,
    required this.onSearch,
  });

  @override
  Widget build(BuildContext context) {
    final status = episodeStatusLine(episode, queueItem);
    return InkWell(
      onTap: onTap,
      onLongPress: onLongPress,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (selecting) ...[
              SizedBox(
                width: 24,
                height: 24,
                child: Checkbox(
                  value: selected,
                  onChanged: (_) => onTap(),
                  activeColor: AppTheme.accent,
                  checkColor: AppTheme.background,
                  side: const BorderSide(color: AppTheme.textSecondary),
                  materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  visualDensity: VisualDensity.compact,
                ),
              ),
              const SizedBox(width: 10),
            ],
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
            if (!selecting)
              IconButton(
                icon: const Icon(Icons.search, color: AppTheme.textSecondary),
                tooltip: 'Automatic search',
                onPressed: onSearch,
              ),
          ],
        ),
      ),
    );
  }
}

/// Selection-mode header: quick-selects (All / Undownloaded / None) and the
/// close (cancel) affordance. The selected count lives in the action bar's
/// "Search N episodes" button.
class _SelectionBar extends StatelessWidget {
  final int selectedCount;
  final VoidCallback onSelectAll;
  final VoidCallback onSelectUndownloaded;
  final VoidCallback onSelectNone;
  final VoidCallback onClose;

  const _SelectionBar({
    required this.selectedCount,
    required this.onSelectAll,
    required this.onSelectUndownloaded,
    required this.onSelectNone,
    required this.onClose,
  });

  @override
  Widget build(BuildContext context) {
    final buttonStyle = TextButton.styleFrom(
      foregroundColor: AppTheme.accent,
      visualDensity: VisualDensity.compact,
      padding: const EdgeInsets.symmetric(horizontal: 10),
      textStyle: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
    );
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        border: Border(bottom: BorderSide(color: AppTheme.border, width: 0.5)),
      ),
      child: Row(
        children: [
          IconButton(
            icon: const Icon(Icons.close, color: AppTheme.textSecondary),
            tooltip: 'Cancel selection',
            onPressed: onClose,
          ),
          Expanded(
            child: Text(
              '$selectedCount selected',
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          TextButton(
              onPressed: onSelectAll,
              style: buttonStyle,
              child: const Text('All')),
          TextButton(
              onPressed: onSelectUndownloaded,
              style: buttonStyle,
              child: const Text('Undownloaded')),
          TextButton(
              onPressed: onSelectNone,
              style: buttonStyle,
              child: const Text('None')),
        ],
      ),
    );
  }
}

class _ActionBar extends StatelessWidget {
  final bool selecting;
  final int selectedCount;
  final int selectedFileCount;
  final VoidCallback onAutomatic;
  final VoidCallback onAutomaticEpisodes;
  final VoidCallback onInteractive;
  final VoidCallback onSearchSelected;
  final VoidCallback onDeleteSelected;

  const _ActionBar({
    required this.selecting,
    required this.selectedCount,
    required this.selectedFileCount,
    required this.onAutomatic,
    required this.onAutomaticEpisodes,
    required this.onInteractive,
    required this.onSearchSelected,
    required this.onDeleteSelected,
  });

  ButtonStyle _buttonStyle({BorderRadius? radius}) => OutlinedButton.styleFrom(
        side: const BorderSide(color: AppTheme.border),
        shape: RoundedRectangleBorder(
            borderRadius: radius ?? BorderRadius.circular(10)),
        padding: const EdgeInsets.symmetric(vertical: 12),
      );

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
            child: selecting ? _buildSearchSelected() : _buildSplitAutomatic(),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: selecting ? _buildDeleteSelected() : _buildInteractive(),
          ),
        ],
      ),
    );
  }

  Widget _buildInteractive() {
    return OutlinedButton.icon(
      onPressed: onInteractive,
      icon: const Icon(Icons.person_outline,
          size: 18, color: AppTheme.available),
      label: const Text('Interactive',
          style: TextStyle(color: AppTheme.textPrimary)),
      style: _buttonStyle(),
    );
  }

  Widget _buildSearchSelected() {
    final enabled = selectedCount > 0;
    return OutlinedButton.icon(
      onPressed: enabled ? onSearchSelected : null,
      icon: Icon(Icons.search,
          size: 18,
          color: enabled ? AppTheme.available : AppTheme.textSecondary),
      label: Text(
        selectedCount == 1
            ? 'Search 1 episode'
            : 'Search $selectedCount episodes',
        style: TextStyle(
            color: enabled ? AppTheme.textPrimary : AppTheme.textSecondary),
      ),
      style: _buttonStyle(),
    );
  }

  /// Deletes the downloaded files among the selected episodes; enabled only
  /// when the selection contains at least one file.
  Widget _buildDeleteSelected() {
    final enabled = selectedFileCount > 0;
    return OutlinedButton.icon(
      onPressed: enabled ? onDeleteSelected : null,
      icon: Icon(Icons.delete_outline,
          size: 18, color: enabled ? AppTheme.error : AppTheme.textSecondary),
      label: Text(
        selectedFileCount == 1
            ? 'Delete 1 file'
            : 'Delete $selectedFileCount files',
        style: TextStyle(
            color: enabled ? AppTheme.error : AppTheme.textSecondary),
      ),
      style: _buttonStyle(),
    );
  }

  /// Split button: the main segment runs the season-wide automatic search, the
  /// arrow reveals "Individual episodes" (selection mode, undownloaded
  /// preselected).
  Widget _buildSplitAutomatic() {
    return IntrinsicHeight(
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Expanded(
            child: OutlinedButton.icon(
              onPressed: onAutomatic,
              icon:
                  const Icon(Icons.search, size: 18, color: AppTheme.available),
              label: const Text('Automatic',
                  style: TextStyle(color: AppTheme.textPrimary)),
              style: _buttonStyle(
                  radius: const BorderRadius.horizontal(
                      left: Radius.circular(10))),
            ),
          ),
          SizedBox(
            width: 34,
            child: MenuAnchor(
              style: const MenuStyle(
                backgroundColor:
                    WidgetStatePropertyAll(AppTheme.surfaceVariant),
              ),
              menuChildren: [
                MenuItemButton(
                  leadingIcon: const Icon(Icons.checklist,
                      size: 18, color: AppTheme.textSecondary),
                  onPressed: onAutomaticEpisodes,
                  child: const Text('Individual episodes',
                      style: TextStyle(
                          color: AppTheme.textPrimary, fontSize: 14)),
                ),
              ],
              builder: (context, controller, _) => OutlinedButton(
                onPressed: () =>
                    controller.isOpen ? controller.close() : controller.open(),
                style: OutlinedButton.styleFrom(
                  side: const BorderSide(color: AppTheme.border),
                  shape: const RoundedRectangleBorder(
                      borderRadius:
                          BorderRadius.horizontal(right: Radius.circular(10))),
                  padding: EdgeInsets.zero,
                  minimumSize: Size.zero,
                ),
                child: const Icon(Icons.arrow_drop_up,
                    size: 20, color: AppTheme.textSecondary),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
