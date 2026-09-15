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
import '../../settings/settings_anchors.dart';

/// Live, resumable setup checklist for admins. Known destinations deep-link
/// to the real settings screen and progress is re-derived from
/// actual configuration on return — a step is "done" because the thing
/// exists, not because a wizard said next. Items the server adds in future
/// versions render automatically (unknown keys get a generic row).
class SetupWizardScreen extends ConsumerStatefulWidget {
  const SetupWizardScreen({super.key});

  @override
  ConsumerState<SetupWizardScreen> createState() => _SetupWizardScreenState();
}

class _SetupWizardScreenState extends ConsumerState<SetupWizardScreen> {
  String? _savingKey;

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
      case 'music':
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
      case 'push':
        return '/settings?highlight=${SettingsAnchors.rootNotifications}';
      default:
        return null; // unknown keys = newer server
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
      case 'music':
        return Icons.library_music_outlined;
      case 'ai':
        return Icons.smart_toy_outlined;
      case 'remediation':
        return Icons.auto_fix_high_outlined;
      default:
        return Icons.tune;
    }
  }

  /// Route extras for the instance rows: the add-instance form opens already
  /// on the service type this row named. Rows that name a category with
  /// several members (download clients, media servers, and the monitoring
  /// row, whose key predates Tracearr) send a selection prompt instead of
  /// guessing one; the form shows it as its disabled placeholder option.
  Map<String, dynamic>? _extraFor(String key) {
    switch (key) {
      case 'radarr':
        return {'service_type': 'radarr'};
      case 'sonarr':
        return {'service_type': 'sonarr'};
      case 'tautulli':
        return {'service_type_prompt': 'Select a monitoring service'};
      case 'books':
        return {'service_type': 'chaptarr'};
      case 'music':
        return {'service_type': 'lidarr'};
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
    if (mounted) ref.read(setupStatusProvider.notifier).refresh();
  }

  /// Records or clears one skip, then re-derives so every surface — the
  /// section counts, the Settings tile, the drawer reminder — follows in the
  /// same breath. Failures are named; a tap that silently changed nothing
  /// would read as the checklist ignoring the admin.
  Future<void> _setSkipped(SetupItem item, bool skipped) async {
    if (_savingKey != null) return;
    setState(() => _savingKey = item.key);
    try {
      await ref.read(setupStatusServiceProvider).setSkipped(item.key, skipped);
      if (mounted) await ref.read(setupStatusProvider.notifier).refresh();
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text(skipped
              ? 'Could not skip "${item.title}". Try again.'
              : 'Could not restore "${item.title}". Try again.')));
    } finally {
      if (mounted) setState(() => _savingKey = null);
    }
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
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                status.summary,
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
                  value: status.progress,
                  minHeight: 6,
                  backgroundColor: AppTheme.border,
                  color:
                      status.isComplete ? AppTheme.available : AppTheme.accent,
                ),
              ),
              const SizedBox(height: 12),
              const Text(
                'Set up the features you want. Skip anything you don\'t use; '
                'you can restore it later. Skips apply to everyone on this server.',
                style: TextStyle(
                    color: AppTheme.textSecondary, fontSize: 13, height: 1.4),
              ),
            ],
          ),
        ),
        _SectionHeader(title: 'Features', remaining: status.remaining),
        ...status.items.map(_buildItem),
        const SizedBox(height: 8),
        const Divider(color: AppTheme.border),
        SwitchListTile(
          value: ref.watch(setupReminderEnabledProvider),
          onChanged: (v) =>
              ref.read(setupReminderEnabledProvider.notifier).set(v),
          secondary: const Icon(Icons.notifications_outlined,
              color: AppTheme.textSecondary),
          title: const Text('Remind me in the menu',
              style: TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
          subtitle: const Text(
              'Show this checklist while items remain to set up or skip',
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
        ),
        const SizedBox(height: 24),
      ],
    );
  }

  /// Configured and skipped rows recede; every other row offers a skip,
  /// including informational rows without a settings destination.
  Widget _buildItem(SetupItem item) {
    final route = _routeFor(item.key);
    final dismissed = item.dismissed;
    final canSkip = item.optional;
    final saving = _savingKey == item.key;
    final actions = <Widget>[];
    if (saving) {
      actions.add(const SizedBox.square(
        dimension: 18,
        child: CircularProgressIndicator(strokeWidth: 2),
      ));
    } else if (item.configured) {
      actions.add(
          const Icon(Icons.check_circle, color: AppTheme.available, size: 20));
    } else if (dismissed) {
      actions.add(_skipAction(item, restore: true));
    } else {
      actions.add(_skipAction(item));
      if (route != null) {
        actions.add(const StatusPill(text: 'Set up', color: AppTheme.accent));
      }
    }

    return LayoutBuilder(builder: (context, constraints) {
      final actionsBelow = constraints.maxWidth < 420 ||
          MediaQuery.textScalerOf(context).scale(14) > 18;
      final actionWidgets = Wrap(spacing: 6, runSpacing: 6, children: actions);
      return ListTile(
        leading: Icon(_iconFor(item.key),
            color: item.configured
                ? AppTheme.available
                : dismissed
                    ? AppTheme.textSecondary
                    : AppTheme.accent),
        title: Text(item.title,
            style: TextStyle(
                color: item.configured || dismissed
                    ? AppTheme.textSecondary
                    : AppTheme.textPrimary,
                fontWeight: FontWeight.w500)),
        subtitle: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(item.description,
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 13)),
            if (!canSkip && !item.configured && !dismissed)
              const Text('Update the server to skip this item.',
                  style:
                      TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
            if (actionsBelow) ...[
              const SizedBox(height: 8),
              actionWidgets,
            ],
          ],
        ),
        trailing: actionsBelow ? null : actionWidgets,
        onTap: route != null
            ? () => _openItem(route, extra: _extraFor(item.key))
            : null,
      );
    });
  }

  Widget _skipAction(SetupItem item, {bool restore = false}) {
    final enabled = _savingKey == null && (restore || item.optional);
    final label = restore ? 'Skipped' : 'Skip';
    return Semantics(
      button: true,
      enabled: enabled,
      child: Tooltip(
        message: restore
            ? 'Restore to the checklist'
            : item.optional
                ? 'Skip this feature for everyone on the server'
                : 'Update the server to skip this item.',
        child: InkWell(
          borderRadius: BorderRadius.circular(12),
          // Absorb a disabled tap so it cannot open the parent settings row.
          onTap: enabled ? () => _setSkipped(item, !restore) : () {},
          excludeFromSemantics: !enabled,
          canRequestFocus: enabled,
          child: StatusPill(
              text: label,
              color: enabled ? AppTheme.textSecondary : AppTheme.textMuted),
        ),
      ),
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
