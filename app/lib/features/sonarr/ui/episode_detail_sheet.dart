import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/status_pill.dart';
import '../../auth/logic/auth_provider.dart';
import '../../issues/ui/report_problem_sheet.dart';
import '../data/sonarr_api_service.dart';
import '../data/sonarr_models.dart';
import 'import_doctor_sheet.dart';
import 'widgets/queue_item_card.dart';

/// Bottom sheet for a single episode: status, overview, the active download (if
/// any) and recent history, with Automatic/Interactive search actions.
class EpisodeDetailSheet extends ConsumerStatefulWidget {
  final String instanceId;
  final SonarrSeries series;
  final SonarrEpisode episode;
  final SonarrQueueItem? queueItem;
  final VoidCallback onAutomaticSearch;
  final VoidCallback onInteractiveSearch;

  const EpisodeDetailSheet({
    super.key,
    required this.instanceId,
    required this.series,
    required this.episode,
    required this.queueItem,
    required this.onAutomaticSearch,
    required this.onInteractiveSearch,
  });

  @override
  ConsumerState<EpisodeDetailSheet> createState() =>
      _EpisodeDetailSheetState();
}

class _EpisodeDetailSheetState extends ConsumerState<EpisodeDetailSheet> {
  List<SonarrHistoryRecord> _history = [];
  bool _loadingHistory = true;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _loadHistory());
  }

  Future<void> _loadHistory() async {
    final service = SonarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    try {
      final all = await service.getSeriesHistory(
        widget.series.id,
        seasonNumber: widget.episode.seasonNumber,
      );
      if (!mounted) return;
      setState(() {
        _history =
            all.where((h) => h.episodeId == widget.episode.id).take(8).toList();
        _loadingHistory = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loadingHistory = false);
    }
  }

  void _openDoctor() {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (_) => ImportDoctorSheet(
        instanceId: widget.instanceId,
        item: widget.queueItem!,
        // The Doctor closes itself; close this episode sheet too (returning
        // true) so the season list refreshes with the result.
        onChanged: () {
          if (mounted) Navigator.of(context).pop(true);
        },
      ),
    );
  }

  ({String label, Color color}) get _shortStatus {
    final q = widget.queueItem;
    if (q != null) {
      return (label: sonarrQueueStatusLabel(q), color: sonarrQueueStatusColor(q));
    }
    final e = widget.episode;
    if (!e.hasFile) {
      return e.hasAired
          ? (label: 'Missing', color: AppTheme.error)
          : (label: 'Unaired', color: AppTheme.downloading);
    }
    final met = !(e.episodeFile?.qualityCutoffNotMet ?? false);
    return (
      label: 'Downloaded',
      color: met ? AppTheme.available : AppTheme.requested
    );
  }

  @override
  Widget build(BuildContext context) {
    final e = widget.episode;
    final status = _shortStatus;
    final airDate = e.airDateUtc?.toLocal() ??
        (e.airDate != null ? DateTime.tryParse(e.airDate!) : null);

    return Padding(
      padding: EdgeInsets.only(bottom: MediaQuery.of(context).viewInsets.bottom),
      child: Container(
        constraints: BoxConstraints(
            maxHeight: MediaQuery.of(context).size.height * 0.85),
        decoration: const BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        ),
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Center(
                child: Container(
                  width: 40,
                  height: 4,
                  decoration: BoxDecoration(
                    color: AppTheme.textSecondary,
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
              ),
              const SizedBox(height: 16),
              Text(
                e.title ?? 'Episode ${e.episodeNumber}',
                style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 20,
                    fontWeight: FontWeight.bold),
              ),
              const SizedBox(height: 6),
              if (airDate != null)
                Text(DateFormat('MMMM d, yyyy').format(airDate),
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 13)),
              Text('${seasonText(e.seasonNumber)} • Episode ${e.episodeNumber}',
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 13)),
              const SizedBox(height: 12),
              StatusPill(text: status.label, color: status.color),
              if (e.overview != null && e.overview!.isNotEmpty) ...[
                const SizedBox(height: 14),
                Text(e.overview!,
                    style: const TextStyle(
                        color: AppTheme.textPrimary, fontSize: 14, height: 1.4)),
              ],
              if (widget.queueItem != null) ...[
                const SizedBox(height: 16),
                SizedBox(
                  width: double.infinity,
                  child: QueueItemCard(
                    item: widget.queueItem!,
                    primaryTitle: widget.queueItem!.title,
                    onShowIssues:
                        widget.queueItem!.hasIssues ? _openDoctor : null,
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
              const SizedBox(height: 16),
              Row(
                children: [
                  Expanded(
                    child: OutlinedButton.icon(
                      onPressed: () {
                        Navigator.of(context).pop();
                        widget.onAutomaticSearch();
                      },
                      icon: const Icon(Icons.search,
                          size: 18, color: AppTheme.available),
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
                      onPressed: () {
                        Navigator.of(context).pop();
                        widget.onInteractiveSearch();
                      },
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
              if (_canReport) ...[
                const SizedBox(height: 10),
                SizedBox(
                  width: double.infinity,
                  child: ReportProblemButton(
                    scope: ReportScope.episode(
                      tmdbId: widget.series.tmdbId ?? 0,
                      tvdbId: widget.series.tvdbId,
                      seasonNumber: e.seasonNumber,
                      episodeNumber: e.episodeNumber,
                      title: widget.series.title,
                    ),
                    // The sheet refreshes the season list by popping true.
                    onSubmitted: () {
                      if (mounted) Navigator.of(context).pop(true);
                    },
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  /// Show "Report a problem" only when the server allows it and we have a
  /// TMDB id to scope the report to.
  bool get _canReport {
    final allow = ref.watch(authProvider).valueOrNull?.connection
            ?.allowReporting ??
        false;
    return allow && (widget.series.tmdbId ?? 0) > 0;
  }

  Widget _buildHistory() {
    if (_loadingHistory) {
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
}

String seasonText(int seasonNumber) =>
    seasonNumber == 0 ? 'Specials' : 'Season $seasonNumber';

String _historyEventLabel(SonarrHistoryRecord r) {
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
    case 'episodeFileDeleted':
      return 'File deleted';
    case 'episodeFileRenamed':
      return 'File renamed';
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
  final SonarrHistoryRecord record;
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
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 11)),
          const SizedBox(height: 2),
          Text(_historyEventLabel(record),
              style: const TextStyle(
                  color: AppTheme.requested, fontSize: 12)),
        ],
      ),
    );
  }
}
