import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/storage/preferences.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../../settings/data/setup_status_service.dart';
import '../../settings/logic/setup_status_provider.dart';

/// Live, resumable setup checklist for admins. Every step deep-links to the
/// real settings screen for that feature and progress is re-derived from
/// actual configuration on return — a step is "done" because the thing
/// exists, not because a wizard said next. Items the server adds in future
/// versions render automatically (unknown keys get a generic row).
class SetupWizardScreen extends ConsumerStatefulWidget {
  const SetupWizardScreen({super.key});

  @override
  ConsumerState<SetupWizardScreen> createState() => _SetupWizardScreenState();
}

class _SetupWizardScreenState extends ConsumerState<SetupWizardScreen> {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(setupStatusProvider.notifier).refresh();
    });
  }

  /// Where each checklist item is configured. Unknown keys (a newer server)
  /// return null and render as informational rows.
  String? _routeFor(String key) {
    switch (key) {
      case 'radarr':
      case 'sonarr':
      case 'download_client':
      case 'tautulli':
      case 'books':
        return '/settings/instance/new';
      case 'tmdb':
      case 'trakt':
      case 'ai':
        return '/settings/credentials';
      case 'plex_invites':
        return '/settings/plex';
      default:
        return null; // push = server env var; unknown keys = newer server
    }
  }

  IconData _iconFor(String key) {
    switch (key) {
      case 'radarr':
        return Icons.movie_outlined;
      case 'sonarr':
        return Icons.tv_outlined;
      case 'tmdb':
        return Icons.search;
      case 'trakt':
        return Icons.trending_up;
      case 'download_client':
        return Icons.download_outlined;
      case 'tautulli':
        return Icons.monitor_heart_outlined;
      case 'push':
        return Icons.notifications_outlined;
      case 'plex_invites':
        return Icons.play_circle_outline;
      case 'books':
        return Icons.menu_book;
      case 'ai':
        return Icons.smart_toy_outlined;
      default:
        return Icons.tune;
    }
  }

  Future<void> _openItem(String? route) async {
    if (route == null) return;
    await context.push(route);
    // Re-derive on return: whatever the admin just configured (or didn't)
    // is reflected immediately.
    ref.read(setupStatusProvider.notifier).refresh();
  }

  @override
  Widget build(BuildContext context) {
    final isAdmin = ref.watch(authProvider).valueOrNull?.user?.isAdmin ?? false;
    final status = ref.watch(setupStatusProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('Setup Checklist')),
      body: CenteredContent(
          child: !isAdmin
              ? const Center(
                  child: Padding(
                    padding: EdgeInsets.all(24),
                    child: Text(
                      'The setup checklist is for server admins.',
                      style: TextStyle(color: AppTheme.textSecondary),
                      textAlign: TextAlign.center,
                    ),
                  ),
                )
              : status == null
                  ? const Center(
                      child: CircularProgressIndicator(color: AppTheme.accent))
                  : RefreshIndicator(
                      onRefresh: () =>
                          ref.read(setupStatusProvider.notifier).refresh(),
                      child: _buildChecklist(status),
                    )),
    );
  }

  Widget _buildChecklist(SetupStatus status) {
    final essentials =
        status.items.where((i) => !i.optional).toList(growable: false);
    final optional =
        status.items.where((i) => i.optional).toList(growable: false);
    final progress = status.total == 0 ? 0.0 : status.configured / status.total;

    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                '${status.configured} of ${status.total} features configured',
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 18,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 8),
              ClipRRect(
                borderRadius: BorderRadius.circular(4),
                child: LinearProgressIndicator(
                  value: progress,
                  minHeight: 6,
                  backgroundColor: AppTheme.border,
                  color: AppTheme.accent,
                ),
              ),
              const SizedBox(height: 12),
              const Text(
                'Each step opens the real settings screen, and progress '
                'reflects what\'s actually configured — come back anytime, '
                'nothing here is one-shot.',
                style: TextStyle(
                    color: AppTheme.textSecondary, fontSize: 13, height: 1.4),
              ),
            ],
          ),
        ),
        const _SectionHeader(title: 'Essentials'),
        ...essentials.map(_buildItem),
        if (optional.isNotEmpty) ...[
          const SizedBox(height: 8),
          const _SectionHeader(title: 'Nice to have'),
          ...optional.map(_buildItem),
        ],
        const SizedBox(height: 8),
        const Divider(color: AppTheme.border),
        SwitchListTile(
          value: ref.watch(setupReminderEnabledProvider),
          onChanged: (v) =>
              ref.read(setupReminderEnabledProvider.notifier).set(v),
          activeThumbColor: AppTheme.accent,
          secondary: const Icon(Icons.notifications_outlined,
              color: AppTheme.textSecondary),
          title: const Text('Remind me in the menu',
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
          subtitle: const Text(
              'Show this checklist in the menu while features remain unconfigured',
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
        ),
        const SizedBox(height: 24),
      ],
    );
  }

  Widget _buildItem(SetupItem item) {
    final route = _routeFor(item.key);
    return ListTile(
      leading: Icon(_iconFor(item.key),
          color: item.configured ? AppTheme.available : AppTheme.textSecondary),
      title: Text(item.title,
          style: const TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
      subtitle: Text(item.description,
          style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
      trailing: item.configured
          ? const Icon(Icons.check_circle, color: AppTheme.available, size: 20)
          : route != null
              ? const Icon(Icons.chevron_right, color: AppTheme.textSecondary)
              : null,
      onTap: route != null ? () => _openItem(route) : null,
    );
  }
}

/// Small uppercase accent header, matching the settings screen sections.
class _SectionHeader extends StatelessWidget {
  final String title;
  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Text(
        title.toUpperCase(),
        style: const TextStyle(
          color: AppTheme.accent,
          fontSize: 12,
          fontWeight: FontWeight.w700,
          letterSpacing: 1.2,
        ),
      ),
    );
  }
}
