import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/downloads_activity.dart';
import '../data/downloads_api_service.dart';
import '../data/downloads_models.dart';
import '../logic/downloads_activity_provider.dart';
import 'downloads_queue_screen.dart';

class DownloadsContentScreen extends ConsumerWidget {
  const DownloadsContentScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (!TickerMode.of(context)) {
      return const SizedBox.shrink();
    }
    final activity = ref.watch(downloadsActivityProvider);
    final admin = ref.watch(authProvider).valueOrNull?.user?.isAdmin == true;
    void refresh() => ref.read(downloadsRefreshProvider.notifier).refresh();
    // Clearing previous data on reload prevents old titles surviving a grant
    // or kids-policy change while the replacement request is in flight.
    if (activity.isLoading) return const Center(child: CircularProgressIndicator());
    if (activity.hasError) {
      return Center(child: Column(mainAxisSize: MainAxisSize.min, children: [
        const Text('Download activity is unavailable.'),
        const SizedBox(height: 8),
        FilledButton.tonal(onPressed: refresh, child: const Text('Retry')),
      ]));
    }
    final value = activity.requireValue;
    return RefreshIndicator(
      onRefresh: () async { refresh(); await ref.read(downloadsActivityProvider.future); },
      child: DownloadsActivityView(activity: value, admin: admin,
        onRefresh: refresh,
        onAction: (job, action, title) async {
          if (!admin || job.control == null) return;
          final control = job.control!;
          bool deleteData = false;
          if (action == 'remove') {
            final confirmed = await showDialog<bool>(context: context,
              builder: (_) => RemoveDownloadDialog(name: title, serviceType: control.serviceType));
            if (confirmed == null || !context.mounted) return;
            deleteData = confirmed;
          }
          final api = DownloadsApiService(backendDio: ref.read(backendClientProvider),
              instanceId: control.instanceId);
          try {
            switch (action) {
              case 'pause': await api.pauseItem(control.itemId);
              case 'resume': await api.resumeItem(control.itemId);
              case 'remove': await api.deleteItem(control.itemId, deleteData: deleteData);
            }
          } catch (_) {
            if (context.mounted) {
              ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Download action failed. Refresh and try again.')));
            }
          } finally {
            if (context.mounted) refresh();
          }
        },
      ),
    );
  }
}

/// Content metadata and job progress are separate: children refer to a job,
/// never invent their own percentage for a pack or album download.
class DownloadsActivityView extends StatelessWidget {
  final DownloadsActivity activity;
  final bool admin;
  final VoidCallback onRefresh;
  final void Function(DownloadActivityJob job, String action, String title) onAction;
  const DownloadsActivityView({super.key, required this.activity, required this.admin,
    required this.onRefresh, required this.onAction});

  @override
  Widget build(BuildContext context) => ListView(
    physics: const AlwaysScrollableScrollPhysics(),
    padding: const EdgeInsets.all(12),
    children: [
      if (!activity.complete || activity.stale) Container(
        margin: const EdgeInsets.only(bottom: 12),
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(color: AppTheme.surface, borderRadius: BorderRadius.circular(12),
            border: Border.all(color: AppTheme.requested)),
        child: Row(children: [
          const Icon(Icons.cloud_off, color: AppTheme.requested, size: 20),
          const SizedBox(width: 10),
          Expanded(child: Text(activity.unavailableSources.isEmpty
              ? 'Some downloads could not be identified. The total is unavailable.'
              : 'Activity is incomplete. Could not read: ${activity.unavailableSources.join(', ')}.')),
          IconButton(tooltip: 'Refresh downloads', onPressed: onRefresh, icon: const Icon(Icons.refresh)),
        ]),
      ),
      if (activity.groups.isEmpty) Padding(
        padding: const EdgeInsets.symmetric(vertical: 56, horizontal: 16),
        child: Column(children: [
          const Icon(Icons.download_outlined, size: 40, color: AppTheme.textSecondary),
          const SizedBox(height: 12),
          Text(activity.complete ? (activity.scope == 'mine'
              ? 'No active downloads for your requests' : 'No active downloads')
              : 'No verified downloads to show yet', textAlign: TextAlign.center),
          const SizedBox(height: 6),
          const Text('Requests waiting for a download and completed jobs are not included.',
              textAlign: TextAlign.center, style: TextStyle(color: AppTheme.textSecondary)),
        ]),
      ),
      for (final group in activity.groups) _ContentRow(group: group, activity: activity,
          admin: admin, onAction: onAction),
    ],
  );
}

class _ContentRow extends StatelessWidget {
  final DownloadContentGroup group;
  final DownloadsActivity activity;
  final bool admin;
  final void Function(DownloadActivityJob, String, String) onAction;
  const _ContentRow({required this.group, required this.activity, required this.admin, required this.onAction});

  @override
  Widget build(BuildContext context) {
    final jobs = [for (final id in group.jobIds) if (activity.jobs[id] != null) activity.jobs[id]!];
    final knownProgress = jobs.isNotEmpty && jobs.every((j) => j.sizeBytes > 0);
    final title = '${group.title}${group.mediaType == 'movie' && group.year > 0 ? ' (${group.year})' : ''}';
    final subtitle = [group.creator, if (group.format.isNotEmpty) group.format == 'audiobook' ? 'Audiobook' : 'Ebook',
      group.instanceName].where((v) => v.isNotEmpty).join(' · ');
    final placeholder = Container(color: AppTheme.surfaceVariant, alignment: Alignment.center,
      child: Icon(switch (group.mediaType) {
        'movie' => Icons.movie_outlined, 'tv' => Icons.tv,
        'book' => Icons.menu_book, 'music' => Icons.album_outlined, _ => Icons.download_outlined,
      }, color: AppTheme.textSecondary));
    Widget childRow(DownloadContentChild child) => Padding(
      padding: const EdgeInsets.symmetric(vertical: 5, horizontal: 16),
      child: Align(alignment: Alignment.centerLeft, child: Text('${child.label}\n'
          '${child.jobIds.map((id) => 'Job ${group.jobIds.indexOf(id) + 1}').join(', ')}',
          style: const TextStyle(fontSize: 13))),
    );
    final seasons = group.children.map((c) => c.season).whereType<int>().toSet().toList()..sort();
    return Card(
      margin: const EdgeInsets.only(bottom: 10),
      color: AppTheme.surface,
      clipBehavior: Clip.antiAlias,
      child: ExpansionTile(
        key: PageStorageKey('download-content-${group.id}'),
        tilePadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        leading: ClipRRect(borderRadius: BorderRadius.circular(6),
          child: SizedBox(width: 42, height: 62, child: group.artwork.isEmpty ? placeholder
              : Image.network(group.artwork, fit: BoxFit.cover,
                  errorBuilder: (context, error, stack) => placeholder))),
        title: Text(title, maxLines: 2, overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w600)),
        subtitle: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          if (subtitle.isNotEmpty) Text(subtitle, maxLines: 2, overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 12, color: AppTheme.textSecondary)),
          const SizedBox(height: 8),
          LinearProgressIndicator(value: knownProgress ? (group.progress / 100).clamp(0, 1) : null,
              minHeight: 4, backgroundColor: AppTheme.surfaceVariant, color: AppTheme.downloading),
          const SizedBox(height: 5),
          Text('${jobs.length} ${jobs.length == 1 ? 'job' : 'jobs'} · '
              '${knownProgress ? '${group.progress.toStringAsFixed(1)}%' : 'Progress unavailable'}',
              style: const TextStyle(fontSize: 12, color: AppTheme.textSecondary)),
        ]),
        children: [
          if (!group.detailsKnown && group.mediaType != 'unmatched') const Padding(
            padding: EdgeInsets.all(12), child: Text('Detailed contents could not be identified.',
                style: TextStyle(color: AppTheme.textSecondary))),
          for (final season in seasons) ExpansionTile(
            key: PageStorageKey('${group.id}:season:$season'),
            title: Text(season == 0 ? 'Specials' : 'Season $season', style: const TextStyle(fontSize: 14)),
            children: [for (final child in group.children.where((c) => c.season == season)) childRow(child)],
          ),
          for (final child in group.children.where((c) => c.season == null)) childRow(child),
          for (var i = 0; i < jobs.length; i++) _JobRow(job: jobs[i],
            label: jobs[i].name.isNotEmpty ? jobs[i].name : 'Job ${i + 1}',
            admin: admin, onAction: (action) => onAction(jobs[i], action, title)),
        ],
      ),
    );
  }
}

class _JobRow extends StatelessWidget {
  final DownloadActivityJob job;
  final String label;
  final bool admin;
  final ValueChanged<String> onAction;
  const _JobRow({required this.job, required this.label, required this.admin, required this.onAction});

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 8, 8, 12),
    child: Row(children: [
      Expanded(child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Text(label, maxLines: 2, overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontWeight: FontWeight.w500, fontSize: 13)),
        Text('${job.status[0].toUpperCase()}${job.status.substring(1)}'
            '${admin && job.control != null ? ' · ${job.control!.clientName}' : ''}',
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
        const SizedBox(height: 7),
        LinearProgressIndicator(value: job.sizeBytes > 0 ? (job.progress / 100).clamp(0, 1) : null,
            minHeight: 4, backgroundColor: AppTheme.surfaceVariant, color: AppTheme.downloading),
        const SizedBox(height: 5),
        Text(job.sizeBytes > 0 ? '${job.progress.toStringAsFixed(1)}% · '
            '${formatBytes(job.sizeBytes - job.sizeLeftBytes)} of ${formatBytes(job.sizeBytes)}'
            '${job.speedBps > 0 ? ' · ${formatSpeed(job.speedBps)}' : ''}' : 'Progress unavailable',
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
        if (admin && job.control == null) const Text('Client controls unavailable',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
      ])),
      if (admin && job.control != null) PopupMenuButton<String>(
        tooltip: 'Actions for $label',
        onSelected: onAction,
        itemBuilder: (_) => [
          PopupMenuItem(value: job.status == 'paused' ? 'resume' : 'pause',
              child: Text(job.status == 'paused' ? 'Resume' : 'Pause')),
          const PopupMenuItem(value: 'remove', child: Text('Remove entire download')),
        ],
      ),
    ]),
  );
}
