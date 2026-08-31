import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/storage/preferences.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/status_pill.dart';
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
      case 'media_servers':
        return '/settings/instance/new';
      case 'media_downloads':
        return '/settings';
      case 'ai':
        return '/settings/credentials';
      case 'tmdb':
      case 'trakt':
      case 'discovery_prefs':
        return '/settings/discovery';
      case 'remediation':
        return '/settings/ai-remediation';
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
      case 'discovery_prefs':
        return Icons.view_carousel_outlined;
      case 'download_client':
        return Icons.download_outlined;
      case 'media_downloads':
        return Icons.download_for_offline_outlined;
      case 'tautulli':
        return Icons.monitor_heart_outlined;
      case 'push':
        return Icons.notifications_outlined;
      case 'media_servers':
        return Icons.live_tv_outlined;
      case 'books':
        return Icons.menu_book;
      case 'ai':
        return Icons.smart_toy_outlined;
      case 'remediation':
        return Icons.auto_fix_high_outlined;
      default:
        return Icons.tune;
    }
  }

  /// Route extras for the instance rows: the add-instance form opens already
  /// on the service type this row named. The download-client row names a
  /// category with four members, so instead of guessing one it sends a
  /// selection prompt the form shows as its disabled placeholder option.
  Map<String, dynamic>? _extraFor(String key) {
    switch (key) {
      case 'radarr':
        return {'service_type': 'radarr'};
      case 'sonarr':
        return {'service_type': 'sonarr'};
      case 'tautulli':
        return {'service_type': 'tautulli'};
      case 'books':
        return {'service_type': 'chaptarr'};
      case 'media_servers':
        return {'service_type_prompt': 'Select a media server'};
      case 'download_client':
        return {'service_type_prompt': 'Select a download client'};
      default:
        return null;
    }
  }

  Future<void> _openItem(String? route, {Object? extra}) async {
    if (route == null) return;
    await context.push(route, extra: extra);
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
        _SectionHeader(
          title: 'Essentials',
          remaining: essentials.where((i) => !i.configured).length,
        ),
        ...essentials.map((i) => _buildItem(i, urgent: status.isUrgent(i))),
        if (optional.isNotEmpty) ...[
          const SizedBox(height: 8),
          _SectionHeader(
            title: 'Nice to have',
            remaining: optional.where((i) => !i.configured).length,
          ),
          ...optional.map((i) => _buildItem(i, urgent: status.isUrgent(i))),
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

  /// One checklist row. The weight goes to what is unfinished: a done row dims
  /// to a receipt, while an outstanding one keeps full-strength copy and ends
  /// in a labelled "Set up" chip rather than the same chevron every navigable
  /// row in the app carries. A chevron only says "this goes somewhere"; the
  /// admin came here to find what still wants doing, and that has to be
  /// markable by scanning the edge of the list instead of reading twelve
  /// descriptions. [urgent] is reserved for rows the server cannot work
  /// without — see [SetupStatus.isUrgent].
  Widget _buildItem(SetupItem item, {required bool urgent}) {
    final route = _routeFor(item.key);
    final actionColor = urgent ? AppTheme.danger : AppTheme.accent;
    return ListTile(
      leading: Icon(_iconFor(item.key),
          color: item.configured ? AppTheme.available : actionColor),
      title: Text(item.title,
          style: TextStyle(
              color: item.configured
                  ? AppTheme.textSecondary
                  : AppTheme.textPrimary,
              fontWeight: FontWeight.w500)),
      subtitle: Text(item.description,
          style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
      // An unconfigured row with nowhere to go (push is a server env var, and
      // unknown keys come from newer servers) gets no chip: there is no action
      // here to offer. Its full-strength title still reads as outstanding.
      trailing: item.configured
          ? const Icon(Icons.check_circle, color: AppTheme.available, size: 20)
          : route != null
              ? StatusPill(text: 'Set up', color: actionColor)
              : null,
      onTap: route != null
          ? () => _openItem(route, extra: _extraFor(item.key))
          : null,
    );
  }
}

/// Small uppercase accent header, matching the settings screen sections, with
/// the section's own outstanding count. The header is the only place the state
/// is legible before scanning rows, and a finished section should read as
/// finished rather than look identical to an empty one.
class _SectionHeader extends StatelessWidget {
  final String title;

  /// How many items in this section are still unconfigured.
  final int remaining;

  const _SectionHeader({required this.title, required this.remaining});

  @override
  Widget build(BuildContext context) {
    const base = TextStyle(
      fontSize: 12,
      fontWeight: FontWeight.w700,
      letterSpacing: 1.2,
    );
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Text.rich(
        TextSpan(
          style: base.copyWith(color: AppTheme.accent),
          children: [
            TextSpan(text: title.toUpperCase()),
            TextSpan(
              // The dot sticks to the title and the count sticks to its word,
              // so a header forced to wrap breaks in the middle rather than
              // leaving a bare "1" hanging on the next line.
              text: remaining > 0
                  ? '\u00A0\u00B7 $remaining\u00A0LEFT'
                  : '\u00A0\u00B7 DONE',
              style: base.copyWith(
                color:
                    remaining > 0 ? AppTheme.textSecondary : AppTheme.available,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
