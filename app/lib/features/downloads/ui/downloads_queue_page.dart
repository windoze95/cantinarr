import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../logic/downloads_activity_provider.dart';
import 'downloads_content_screen.dart';
import 'downloads_queue_screen.dart';

class DownloadsQueuePage extends ConsumerWidget {
  const DownloadsQueuePage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final auth = ref.watch(authProvider).valueOrNull;
    final admin = auth?.user?.isAdmin == true;
    if (auth?.connection?.downloadsActivity != true) {
      return admin ? const DownloadsQueueScreen() : const SizedBox.shrink();
    }
    final preferences = ref.watch(downloadsPreferencesProvider);
    final selection = preferences.valueOrNull;
    final scope = ref.watch(downloadsScopeProvider);
    final summary = ref.watch(downloadsSummaryProvider);
    final restricted = auth?.connection?.downloadsUserScope == 'mine' ||
        summary.valueOrNull?.userScope == 'mine';
    if (preferences.isLoading || selection == null) {
      return const Center(child: CircularProgressIndicator());
    }
    final content = !admin || selection.content;
    return Column(children: [
      Padding(
        padding: const EdgeInsets.fromLTRB(16, 8, 12, 8),
        child: Row(children: [
          if (admin)
            SegmentedButton<bool>(
              segments: [
                ButtonSegment(
                    value: true, label: Text(context.l10n.adminContent)),
                ButtonSegment(
                    value: false, label: Text(context.l10n.adminClients))
              ],
              selected: {content},
              showSelectedIcon: false,
              onSelectionChanged: (v) => ref
                  .read(downloadsPreferencesProvider.notifier)
                  .select(content: v.single),
            )
          else if (!restricted)
            Flexible(
                child: SegmentedButton<String>(
              segments: [
                ButtonSegment(
                    value: 'all', label: Text(context.l10n.adminAllDownloads)),
                ButtonSegment(
                    value: 'mine', label: Text(context.l10n.adminMyRequests))
              ],
              selected: {scope},
              showSelectedIcon: false,
              onSelectionChanged: (v) => ref
                  .read(downloadsPreferencesProvider.notifier)
                  .select(scope: v.single),
            ))
          else
            Text(context.l10n.adminMyRequests,
                style: TextStyle(fontWeight: FontWeight.w600)),
          const Spacer(),
          if (admin)
            PopupMenuButton<String>(
              tooltip: context.l10n.adminDownloadsSettings,
              icon: const Icon(Icons.more_vert),
              onSelected: (value) async {
                try {
                  await ref.read(backendClientProvider).put(
                      '/api/admin/downloads/settings',
                      data: {'user_scope': value});
                  await ref.read(authProvider.notifier).refreshConfig();
                  ref.read(downloadsRefreshProvider.notifier).refresh();
                } catch (_) {
                  if (context.mounted) {
                    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
                        content: Text(context.l10n
                            .adminCouldNotSaveDownloadVisibilityTryAgain)));
                  }
                }
              },
              itemBuilder: (_) => [
                PopupMenuItem(
                    enabled: false,
                    child: Text(context.l10n.adminUserVisibility)),
                CheckedPopupMenuItem(
                    value: 'mine',
                    checked: restricted,
                    child: Text(context.l10n.adminOwnRequestsOnly)),
                CheckedPopupMenuItem(
                    value: 'all',
                    checked: !restricted,
                    child: Text(context.l10n.adminAllAccessibleDownloads)),
              ],
            ),
        ]),
      ),
      const Divider(height: 1, color: AppTheme.border),
      Expanded(
          child: content
              ? const DownloadsContentScreen()
              : const DownloadsQueueScreen()),
    ]);
  }
}
