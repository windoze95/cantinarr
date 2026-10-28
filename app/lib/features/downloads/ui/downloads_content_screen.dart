import 'package:cantinarr/l10n/l10n.dart';
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
    final value = activity.valueOrNull;
    if (value == null && activity.isLoading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (value == null && activity.hasError) {
      return Center(
          child: Column(mainAxisSize: MainAxisSize.min, children: [
        Text(context.l10n.adminDownloadActivityIsUnavailable),
        const SizedBox(height: 8),
        FilledButton.tonal(
            onPressed: refresh, child: Text(context.l10n.adminRetry)),
      ]));
    }
    return RefreshIndicator(
      onRefresh: () async {
        refresh();
        await ref.read(downloadsActivityFutureProvider);
      },
      child: DownloadsActivityView(
        activity: value!,
        admin: admin,
        refreshFailed: activity.hasError,
        onRefresh: refresh,
        onAction: (job, action, title) async {
          if (!admin || job.control == null) return;
          final control = job.control!;
          bool deleteData = false;
          if (action == 'remove') {
            final confirmed = await showDialog<bool>(
                context: context,
                builder: (_) => RemoveDownloadDialog(
                    name: title, serviceType: control.serviceType));
            if (confirmed == null || !context.mounted) return;
            deleteData = confirmed;
          }
          final api = DownloadsApiService(
              backendDio: ref.read(backendClientProvider),
              instanceId: control.instanceId);
          try {
            switch (action) {
              case 'pause':
                await api.pauseItem(control.itemId);
              case 'resume':
                await api.resumeItem(control.itemId);
              case 'remove':
                await api.deleteItem(control.itemId, deleteData: deleteData);
            }
          } catch (_) {
            if (context.mounted) {
              ScaffoldMessenger.of(context).showSnackBar(SnackBar(
                  content: Text(context
                      .l10n.adminDownloadActionFailedRefreshAndTryAgain)));
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
  final bool refreshFailed;
  final VoidCallback onRefresh;
  final void Function(DownloadActivityJob job, String action, String title)
      onAction;
  const DownloadsActivityView(
      {super.key,
      required this.activity,
      required this.admin,
      this.refreshFailed = false,
      required this.onRefresh,
      required this.onAction});

  @override
  Widget build(BuildContext context) => ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.all(12),
        children: [
          if (refreshFailed)
            Container(
              margin: const EdgeInsets.only(bottom: 12),
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                  color: AppTheme.surface,
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(color: AppTheme.requested)),
              child: Row(children: [
                const Icon(Icons.cloud_off,
                    color: AppTheme.requested, size: 20),
                const SizedBox(width: 10),
                Expanded(
                    child: Text(context
                        .l10n.adminCouldNotRefreshDownloadsShowingThePrevious)),
                IconButton(
                    tooltip: context.l10n.adminRefreshDownloads,
                    onPressed: onRefresh,
                    icon: const Icon(Icons.refresh)),
              ]),
            ),
          if (!activity.complete || activity.stale)
            Container(
              margin: const EdgeInsets.only(bottom: 12),
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                  color: AppTheme.surface,
                  borderRadius: BorderRadius.circular(12),
                  border: Border.all(color: AppTheme.requested)),
              child: Row(children: [
                const Icon(Icons.cloud_off,
                    color: AppTheme.requested, size: 20),
                const SizedBox(width: 10),
                Expanded(
                    child: Text(activity.unavailableSources.isEmpty
                        ? context.l10n.adminSomeDownloadsCouldNotBeIdentifiedThe
                        : context.l10n.adminActivityIsIncompleteCouldNotRead(
                            (activity.unavailableSources.join(', '))
                                .toString()))),
                IconButton(
                    tooltip: context.l10n.adminRefreshDownloads,
                    onPressed: onRefresh,
                    icon: const Icon(Icons.refresh)),
              ]),
            ),
          if (activity.groups.isEmpty)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 56, horizontal: 16),
              child: Column(children: [
                const Icon(Icons.download_outlined,
                    size: 40, color: AppTheme.textSecondary),
                const SizedBox(height: 12),
                Text(
                    activity.complete
                        ? (activity.scope == 'mine'
                            ? context.l10n.adminNoActiveDownloadsForYourRequests
                            : context.l10n.adminNoActiveDownloads)
                        : context.l10n.adminNoVerifiedDownloadsToShowYet,
                    textAlign: TextAlign.center),
                const SizedBox(height: 6),
                Text(context.l10n.adminRequestsWaitingForADownloadAndCompleted,
                    textAlign: TextAlign.center,
                    style: TextStyle(color: AppTheme.textSecondary)),
              ]),
            ),
          for (final group in activity.groups)
            _ContentRow(
                group: group,
                activity: activity,
                admin: admin,
                onAction: onAction),
        ],
      );
}

class _ContentRow extends StatelessWidget {
  final DownloadContentGroup group;
  final DownloadsActivity activity;
  final bool admin;
  final void Function(DownloadActivityJob, String, String) onAction;
  const _ContentRow(
      {required this.group,
      required this.activity,
      required this.admin,
      required this.onAction});

  @override
  Widget build(BuildContext context) {
    final jobs = [
      for (final id in group.jobIds)
        if (activity.jobs[id] != null) activity.jobs[id]!
    ];
    final knownProgress = jobs.isNotEmpty && jobs.every((j) => j.sizeBytes > 0);
    // One download is the common case, so its status and bytes belong on the
    // title row itself. Numbered downloads only exist to tell packs apart.
    final multi = jobs.length > 1;
    String jobLabel(String id) =>
        context.l10n.adminDownload((group.jobIds.indexOf(id) + 1).toString());
    final progress = knownProgress
        ? '${group.progress.toStringAsFixed(1)}%'
        : context.l10n.adminProgressUnavailable;
    final summary = multi
        ? context.l10n
            .adminDownloads((jobs.length).toString(), (progress).toString())
        : jobs.isEmpty
            ? progress
            : '${statusLabel(jobs.single, strings: context.l10n)} · $progress${knownProgress ? ' · ${context.l10n.polishDownloadBytes(formatBytes(jobs.single.sizeBytes - jobs.single.sizeLeftBytes), formatBytes(jobs.single.sizeBytes))}' : ''}';
    final title =
        '${group.displayTitle}${group.mediaType == 'movie' && group.year > 0 ? ' (${group.year})' : ''}';
    final subtitle = [
      group.creator,
      if (group.format.isNotEmpty)
        group.format == 'audiobook'
            ? context.l10n.adminAudiobook
            : context.l10n.adminEbook,
      group.instanceName
    ].where((v) => v.isNotEmpty).join(' · ');
    final placeholder = Container(
        color: AppTheme.surfaceVariant,
        alignment: Alignment.center,
        child: Icon(
            switch (group.mediaType) {
              'movie' => Icons.movie_outlined,
              'tv' => Icons.tv,
              'book' => Icons.menu_book,
              'music' => Icons.album_outlined,
              _ => Icons.download_outlined,
            },
            color: AppTheme.textSecondary));
    Widget childRow(DownloadContentChild child) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 5, horizontal: 16),
          child: Align(
              alignment: Alignment.centerLeft,
              child: Text(
                  multi
                      ? '${child.label}\n${child.jobIds.map(jobLabel).join(', ')}'
                      : child.label,
                  style: const TextStyle(fontSize: 13))),
        );
    final seasons = group.children
        .map((c) => c.season)
        .whereType<int>()
        .toSet()
        .toList()
      ..sort();
    return Card(
      margin: const EdgeInsets.only(bottom: 10),
      color: AppTheme.surface,
      clipBehavior: Clip.antiAlias,
      child: ExpansionTile(
        key: PageStorageKey('download-content-${group.id}'),
        tilePadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        leading: ClipRRect(
            borderRadius: BorderRadius.circular(6),
            child: SizedBox(
                width: 42,
                height: 62,
                child: group.displayArtwork.isEmpty
                    ? placeholder
                    : Image.network(group.displayArtwork,
                        fit: BoxFit.cover,
                        errorBuilder: (context, error, stack) => placeholder))),
        title: Text(title,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w600)),
        subtitle:
            Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          if (subtitle.isNotEmpty)
            Text(subtitle,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                    fontSize: 12, color: AppTheme.textSecondary)),
          const SizedBox(height: 8),
          LinearProgressIndicator(
              value: knownProgress ? (group.progress / 100).clamp(0, 1) : null,
              minHeight: 4,
              backgroundColor: AppTheme.surfaceVariant,
              color: AppTheme.downloading),
          const SizedBox(height: 5),
          Text(summary,
              style:
                  const TextStyle(fontSize: 12, color: AppTheme.textSecondary)),
        ]),
        children: [
          if (!group.detailsKnown &&
              group.mediaType != 'unmatched' &&
              group.mediaType != 'unknown')
            Padding(
                padding: EdgeInsets.all(12),
                child: Text(
                    context.l10n.adminDetailedContentsCouldNotBeIdentified,
                    style: TextStyle(color: AppTheme.textSecondary))),
          for (final season in seasons)
            ExpansionTile(
              key: PageStorageKey('${group.id}:season:$season'),
              title: Text(
                  season == 0
                      ? context.l10n.adminSpecials
                      : context.l10n.adminSeason((season).toString()),
                  style: const TextStyle(fontSize: 14)),
              children: [
                for (final child
                    in group.children.where((c) => c.season == season))
                  childRow(child)
              ],
            ),
          for (final child in group.children.where((c) => c.season == null))
            childRow(child),
          // A requester's single download is already summarised on the title
          // row; admins keep the row for the client name and its actions.
          if (admin || multi)
            for (final job in jobs)
              _JobRow(
                  job: job,
                  label: job.name.isNotEmpty
                      ? job.name
                      : multi
                          ? jobLabel(job.id)
                          : null,
                  name: job.name.isNotEmpty
                      ? job.name
                      : multi
                          ? jobLabel(job.id)
                          : title,
                  admin: admin,
                  onAction: (action) => onAction(job, action, title)),
        ],
      ),
    );
  }
}

String statusLabel(DownloadActivityJob job, {AppLocalizations? strings}) => job
        .status.isEmpty
    ? (strings ?? lookupAppLocalizations(const Locale('en'))).adminDownloading
    : '${job.status[0].toUpperCase()}${job.status.substring(1)}';

class _JobRow extends StatelessWidget {
  final DownloadActivityJob job;

  /// The job's own name when the server sent one, a "Download N" tag when the
  /// group has several, otherwise nothing: the status line leads.
  final String? label;

  /// What the actions menu calls this job.
  final String name;
  final bool admin;
  final ValueChanged<String> onAction;
  const _JobRow(
      {required this.job,
      required this.label,
      required this.name,
      required this.admin,
      required this.onAction});

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 8, 8, 12),
        child: Row(children: [
          Expanded(
              child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                if (label != null)
                  Text(label!,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                          fontWeight: FontWeight.w500, fontSize: 13)),
                Text(
                    '${statusLabel(job, strings: context.l10n)}${admin && job.control != null ? ' · ${job.control!.clientName}' : ''}',
                    style: label == null
                        ? const TextStyle(
                            fontWeight: FontWeight.w500, fontSize: 13)
                        : const TextStyle(
                            color: AppTheme.textSecondary, fontSize: 12)),
                const SizedBox(height: 7),
                LinearProgressIndicator(
                    value: job.sizeBytes > 0
                        ? (job.progress / 100).clamp(0, 1)
                        : null,
                    minHeight: 4,
                    backgroundColor: AppTheme.surfaceVariant,
                    color: AppTheme.downloading),
                const SizedBox(height: 5),
                Text(
                    job.sizeBytes > 0
                        ? context.l10n.adminOf(
                            (job.progress.toStringAsFixed(1)).toString(),
                            (formatBytes(job.sizeBytes - job.sizeLeftBytes))
                                .toString(),
                            (formatBytes(job.sizeBytes)).toString(),
                            (job.speedBps > 0
                                    ? ' · ${formatSpeed(job.speedBps)}'
                                    : '')
                                .toString())
                        : context.l10n.adminProgressUnavailable,
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 12)),
                if (admin && job.control == null)
                  Text(context.l10n.adminClientControlsUnavailable,
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12)),
              ])),
          if (admin && job.control != null)
            PopupMenuButton<String>(
              tooltip: context.l10n.adminActionsFor((name).toString()),
              onSelected: onAction,
              itemBuilder: (_) => [
                PopupMenuItem(
                    value: job.status == 'paused' ? 'resume' : 'pause',
                    child: Text(job.status == 'paused'
                        ? context.l10n.adminResume
                        : context.l10n.adminPause)),
                PopupMenuItem(
                    value: 'remove',
                    child: Text(context.l10n.adminRemoveEntireDownload)),
              ],
            ),
        ]),
      );
}
