import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/error_banner.dart';
import '../../../core/widgets/status_pill.dart';
import '../../../navigation/ambient_page_route.dart';
import '../../auth/logic/auth_provider.dart';
import '../../issues/ui/report_problem_sheet.dart';
import '../../media_download/ui/media_download_button.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';
import 'radarr_import_doctor_sheet.dart';
import 'radarr_releases_screen.dart';
import 'widgets/radarr_queue_item_card.dart';

/// Movie detail: poster/overview, the downloaded file's quality+size, the
/// active download (with the Import Doctor affordance) and recent history, plus
/// an Automatic/Interactive search action bar. The movie-side mirror of the
/// Sonarr series → season → episode drill-down (movies are single items, so the
/// whole picture fits on one screen).
class RadarrMovieDetailScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final RadarrMovie movie;

  const RadarrMovieDetailScreen({
    super.key,
    required this.instanceId,
    required this.movie,
  });

  @override
  ConsumerState<RadarrMovieDetailScreen> createState() =>
      _RadarrMovieDetailScreenState();
}

class _RadarrMovieDetailScreenState
    extends ConsumerState<RadarrMovieDetailScreen> {
  late final RadarrApiService _service;
  late RadarrMovie _movie;
  RadarrQueueItem? _queueItem;
  List<RadarrHistoryRecord> _history = [];
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _movie = widget.movie;
    _service = RadarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    setState(() => _isLoading = true);
    try {
      // Kick off in parallel, then await — the movie + its queue item + history.
      final movieFuture = _service.getMovieById(_movie.id);
      final queueFuture = _service.getQueueDetailed();
      final historyFuture = _service.getMovieHistory(_movie.id);
      final movie = await movieFuture;
      final queue = await queueFuture;
      final history = await historyFuture;
      if (!mounted) return;
      setState(() {
        _movie = movie;
        _queueItem = queue.where((q) => q.movieId == _movie.id).isNotEmpty
            ? queue.firstWhere((q) => q.movieId == _movie.id)
            : null;
        _history = history.take(8).toList();
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load movie: $e';
      });
    }
  }

  Future<void> _automaticSearch() async {
    try {
      await _service.searchMovie(_movie.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Searching for ${_movie.title}…')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Search failed: $e')));
    }
  }

  void _interactiveSearch() {
    Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => RadarrReleasesScreen(
          instanceId: widget.instanceId,
          movieId: _movie.id,
          movieTitle: _movie.title,
        ),
      ),
    );
  }

  void _openDoctor() {
    final item = _queueItem;
    if (item == null) return;
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (_) => RadarrImportDoctorSheet(
        instanceId: widget.instanceId,
        item: item,
        onChanged: _load,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppTheme.background,
      appBar: AppBar(
        backgroundColor: AppTheme.background,
        title: Text(_movie.title, maxLines: 1, overflow: TextOverflow.ellipsis),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh, color: AppTheme.textPrimary),
            tooltip: 'Refresh',
            onPressed: _isLoading ? null : _load,
          ),
        ],
      ),
      body: CenteredContent(
          child: Column(
        children: [
          Expanded(child: _buildBody()),
          _ActionBar(
            onAutomatic: _automaticSearch,
            onInteractive: _interactiveSearch,
          ),
        ],
      )),
    );
  }

  Widget _buildBody() {
    if (_isLoading && _history.isEmpty && _queueItem == null) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null) {
      return FullScreenError(message: _error!, onRetry: _load);
    }
    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: ListView(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 16),
        children: [
          _Header(movie: _movie),
          const SizedBox(height: 16),
          StatusPill(text: _statusLabel, color: _statusColor),
          if (_movie.overview != null && _movie.overview!.isNotEmpty) ...[
            const SizedBox(height: 14),
            Text(_movie.overview!,
                style: const TextStyle(
                    color: AppTheme.textPrimary, fontSize: 14, height: 1.4)),
          ],
          if (_movie.movieFile != null) ...[
            const SizedBox(height: 16),
            _FileCard(
              instanceId: widget.instanceId,
              file: _movie.movieFile!,
            ),
          ],
          if (_queueItem != null) ...[
            const SizedBox(height: 16),
            RadarrQueueItemCard(
              item: _queueItem!,
              primaryTitle: _queueItem!.title,
              onShowIssues: _queueItem!.hasIssues ? _openDoctor : null,
            ),
          ],
          if (_canReport) ...[
            const SizedBox(height: 16),
            SizedBox(
              width: double.infinity,
              child: ReportProblemButton(
                scope: ReportScope.movie(
                  instanceId: widget.instanceId,
                  tmdbId: _movie.tmdbId ?? 0,
                  title: _movie.title,
                ),
                onSubmitted: _load,
              ),
            ),
          ],
          const SizedBox(height: 16),
          const Text('History',
              style: TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 13,
                  fontWeight: FontWeight.w600)),
          const SizedBox(height: 8),
          _buildHistory(),
        ],
      ),
    );
  }

  Widget _buildHistory() {
    if (_isLoading && _history.isEmpty) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 12),
        child: Center(
            child: SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(
                    strokeWidth: 2, color: AppTheme.accent))),
      );
    }
    if (_history.isEmpty) {
      return const Text('No history yet.',
          style: TextStyle(color: AppTheme.textSecondary, fontSize: 13));
    }
    return Column(
      children: _history.map((h) => _HistoryTile(record: h)).toList(),
    );
  }

  String get _statusLabel {
    if (_movie.hasFile) {
      final met = !(_movie.movieFile?.qualityCutoffNotMet ?? false);
      return met ? 'Downloaded' : 'Upgrade available';
    }
    if (!_movie.monitored) return 'Unmonitored';
    return _movie.isAvailable ? 'Missing' : 'Not yet available';
  }

  Color get _statusColor {
    if (_movie.hasFile) {
      final met = !(_movie.movieFile?.qualityCutoffNotMet ?? false);
      return met ? AppTheme.available : AppTheme.requested;
    }
    if (!_movie.monitored) return AppTheme.unavailable;
    return _movie.isAvailable ? AppTheme.error : AppTheme.downloading;
  }

  /// Show "Report a problem" only when the server allows it and we have a
  /// TMDB id to scope the report to.
  bool get _canReport {
    final allow =
        ref.watch(authProvider).valueOrNull?.connection?.allowReporting ??
            false;
    return allow && (_movie.tmdbId ?? 0) > 0;
  }
}

class _Header extends StatelessWidget {
  final RadarrMovie movie;
  const _Header({required this.movie});

  @override
  Widget build(BuildContext context) {
    final meta = <String>[
      if (movie.year > 0) '${movie.year}',
      if (movie.runtime > 0) '${movie.runtime} min',
      if (movie.sizeOnDisk > 0 || movie.movieFile?.size != null)
        movie.sizeOnDiskFormatted,
    ];
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(8),
          child: SizedBox(
            width: 100,
            height: 150,
            child: CachedImage(
              url: movie.posterUrl,
              fit: BoxFit.cover,
              icon: Icons.movie,
              iconSize: 28,
            ),
          ),
        ),
        const SizedBox(width: 14),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(movie.title,
                  style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 20,
                      fontWeight: FontWeight.bold)),
              const SizedBox(height: 6),
              if (meta.isNotEmpty)
                Text(meta.join(' • '),
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 13)),
              if (movie.ratings != null) ...[
                const SizedBox(height: 6),
                Row(
                  children: [
                    const Icon(Icons.star, size: 14, color: AppTheme.accent),
                    const SizedBox(width: 4),
                    Text(movie.ratings!.toStringAsFixed(1),
                        style: const TextStyle(
                            color: AppTheme.textSecondary, fontSize: 13)),
                  ],
                ),
              ],
            ],
          ),
        ),
      ],
    );
  }
}

/// The downloaded file's quality + size line — Radarr's analogue of the Sonarr
/// per-episode "WEBDL-1080p — 4.3 GB" status.
class _FileCard extends ConsumerWidget {
  final String instanceId;
  final RadarrMovieFile file;
  const _FileCard({required this.instanceId, required this.file});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final cutoffMet = !file.qualityCutoffNotMet;
    final connection = ref.watch(authProvider).valueOrNull?.connection;
    final downloadsEnabled = connection?.services.mediaDownloads ?? false;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border, width: 0.5),
      ),
      child: Row(
        children: [
          Icon(Icons.movie_creation_outlined,
              color: cutoffMet ? AppTheme.available : AppTheme.requested,
              size: 22),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    StatusPill(
                      text: file.quality ?? 'Downloaded',
                      color:
                          cutoffMet ? AppTheme.available : AppTheme.requested,
                    ),
                    if (!cutoffMet) ...[
                      const SizedBox(width: 6),
                      const StatusPill(
                          text: 'Upgrade available', color: AppTheme.requested),
                    ],
                  ],
                ),
                const SizedBox(height: 6),
                Text(
                  [
                    file.sizeFormatted,
                    if (file.releaseGroup != null &&
                        file.releaseGroup!.isNotEmpty)
                      file.releaseGroup!,
                  ].join(' • '),
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 12),
                ),
                if (file.relativePath != null &&
                    file.relativePath!.isNotEmpty) ...[
                  const SizedBox(height: 2),
                  Text(file.relativePath!,
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 11),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis),
                ],
              ],
            ),
          ),
          if (downloadsEnabled && file.id > 0)
            MediaDownloadButton(
              instanceId: instanceId,
              fileId: file.id,
              label: 'Download movie file',
              iconOnly: true,
            ),
        ],
      ),
    );
  }
}

String _historyEventLabel(RadarrHistoryRecord r) {
  switch (r.eventType) {
    case 'grabbed':
      final indexer = r.indexer;
      return indexer != null && indexer.isNotEmpty
          ? 'Grabbed from $indexer'
          : 'Grabbed';
    case 'downloadFolderImported':
      return 'Imported';
    case 'downloadFailed':
      return 'Download failed';
    case 'movieFileDeleted':
      return 'File deleted';
    case 'movieFileRenamed':
      return 'File renamed';
    case 'movieAdded':
      return 'Added';
    case 'downloadIgnored':
      return 'Download ignored';
    default:
      return r.eventType.isEmpty ? 'Event' : r.eventType;
  }
}

String _historyTime(DateTime? date) {
  if (date == null) return '';
  final local = date.toLocal();
  final diff = DateTime.now().difference(local);
  final String rel;
  if (diff.inMinutes < 1) {
    rel = 'Just now';
  } else if (diff.inMinutes < 60) {
    rel = '${diff.inMinutes}m ago';
  } else if (diff.inHours < 24) {
    rel = '${diff.inHours}h ago';
  } else if (diff.inDays < 30) {
    rel = '${diff.inDays}d ago';
  } else {
    rel = DateFormat('MMM d, yyyy').format(local);
  }
  return '$rel • ${DateFormat('MMM d, yyyy • h:mm a').format(local)}';
}

class _HistoryTile extends StatelessWidget {
  final RadarrHistoryRecord record;
  const _HistoryTile({required this.record});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            record.sourceTitle.isNotEmpty
                ? record.sourceTitle
                : _historyEventLabel(record),
            style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                fontWeight: FontWeight.w500),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
          const SizedBox(height: 2),
          Text(_historyTime(record.date),
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 11)),
          const SizedBox(height: 2),
          Text(_historyEventLabel(record),
              style: const TextStyle(color: AppTheme.requested, fontSize: 12)),
        ],
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
              icon:
                  const Icon(Icons.search, size: 18, color: AppTheme.available),
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
