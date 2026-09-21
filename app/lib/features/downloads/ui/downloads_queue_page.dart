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
              segments: const [ButtonSegment(value: true, label: Text('Content')),
                ButtonSegment(value: false, label: Text('Clients'))],
              selected: {content},
              showSelectedIcon: false,
              onSelectionChanged: (v) => ref.read(downloadsPreferencesProvider.notifier)
                  .select(content: v.single),
            )
          else if (!restricted)
            Flexible(child: SegmentedButton<String>(
              segments: const [ButtonSegment(value: 'all', label: Text('All downloads')),
                ButtonSegment(value: 'mine', label: Text('My requests'))],
              selected: {scope},
              showSelectedIcon: false,
              onSelectionChanged: (v) => ref.read(downloadsPreferencesProvider.notifier)
                  .select(scope: v.single),
            ))
          else
            const Text('My requests', style: TextStyle(fontWeight: FontWeight.w600)),
          const Spacer(),
          if (admin) PopupMenuButton<String>(
            tooltip: 'Downloads settings',
            icon: const Icon(Icons.more_vert),
            onSelected: (value) async {
              try {
                await ref.read(backendClientProvider).put('/api/admin/downloads/settings',
                    data: {'user_scope': value});
                await ref.read(authProvider.notifier).refreshConfig();
                ref.read(downloadsRefreshProvider.notifier).refresh();
              } catch (_) {
                if (context.mounted) {
                  ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(content: Text('Could not save download visibility. Try again.')));
                }
              }
            },
            itemBuilder: (_) => [
              const PopupMenuItem(enabled: false, child: Text('User visibility')),
              CheckedPopupMenuItem(value: 'mine', checked: restricted,
                  child: const Text('Own requests only')),
              CheckedPopupMenuItem(value: 'all', checked: !restricted,
                  child: const Text('All accessible downloads')),
            ],
          ),
        ]),
      ),
      const Divider(height: 1, color: AppTheme.border),
      Expanded(child: content ? const DownloadsContentScreen() : const DownloadsQueueScreen()),
    ]);
  }
}
