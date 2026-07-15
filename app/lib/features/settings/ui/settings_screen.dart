import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:package_info_plus/package_info_plus.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/storage/preferences.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../../../core/widgets/attention_menu_visibility_switch.dart';
import '../../ai_assistant/data/ai_settings_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../logic/setup_status_provider.dart';
import '../logic/update_status_provider.dart';
import 'about_sheet.dart';

/// Simplified settings screen for backend-connected architecture.
class SettingsScreen extends ConsumerStatefulWidget {
  const SettingsScreen({super.key});

  @override
  ConsumerState<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends ConsumerState<SettingsScreen> {
  String _appVersion = '';

  @override
  void initState() {
    super.initState();
    PackageInfo.fromPlatform().then((info) {
      final build = info.buildNumber;
      setState(() => _appVersion =
          build.isNotEmpty ? '${info.version} ($build)' : info.version);
    });
    // Learn whether the account has a password so the Account tile reflects
    // it, and re-derive the setup checklist so its tile subtitle is current.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(authProvider.notifier).refreshUser();
      ref.read(setupStatusProvider.notifier).refresh();
      ref.read(updateStatusProvider.notifier).refresh();
    });
  }

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);
    final auth = authState.valueOrNull;
    final connection = auth?.connection;
    final user = auth?.user;
    final instances = connection?.instances ?? [];
    final setupStatus = ref.watch(setupStatusProvider);
    final updateStatus = ref.watch(updateStatusProvider);
    final aiSettings = ref.watch(aiSettingsProvider).valueOrNull;
    final aiAvailable =
        aiSettings?.effective.available ?? connection?.services.ai ?? false;

    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: CenteredContent(
          child: ListView(
        padding: const EdgeInsets.symmetric(vertical: 8),
        children: [
          AppPanel(
            margin: const EdgeInsets.fromLTRB(16, 8, 16, 18),
            padding: const EdgeInsets.all(18),
            accentColor: AppTheme.signal,
            child: Row(
              children: [
                Container(
                  width: 48,
                  height: 48,
                  decoration: BoxDecoration(
                    gradient: LinearGradient(
                      begin: Alignment.topLeft,
                      end: Alignment.bottomRight,
                      colors: [
                        AppTheme.signal.withValues(alpha: 0.2),
                        AppTheme.accent.withValues(alpha: 0.11),
                      ],
                    ),
                    borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
                    border: Border.all(
                      color: AppTheme.signal.withValues(alpha: 0.22),
                    ),
                  ),
                  child: const Icon(
                    Icons.tune_rounded,
                    color: AppTheme.signal,
                  ),
                ),
                const SizedBox(width: 14),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        'Settings overview',
                        style:
                            Theme.of(context).textTheme.titleMedium?.copyWith(
                                  color: AppTheme.textPrimary,
                                  fontWeight: FontWeight.w700,
                                ),
                      ),
                      const SizedBox(height: 3),
                      Text(
                        '${user?.username ?? 'Account'}  /  ${connection?.serverName ?? 'Cantinarr'}',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.bodySmall,
                      ),
                    ],
                  ),
                ),
                Container(
                  width: 9,
                  height: 9,
                  decoration: BoxDecoration(
                    color: auth?.isAuthenticated == true
                        ? AppTheme.available
                        : AppTheme.error,
                    shape: BoxShape.circle,
                    boxShadow: [
                      BoxShadow(
                        color: (auth?.isAuthenticated == true
                                ? AppTheme.available
                                : AppTheme.error)
                            .withValues(alpha: 0.28),
                        blurRadius: 10,
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
          // Server connection
          const _SectionHeader(title: 'Server'),
          _SettingsTile(
            icon: Icons.dns_outlined,
            title: connection?.serverName ?? 'Cantinarr',
            subtitle: connection?.serverUrl ?? 'Not connected',
          ),
          _SettingsTile(
            icon: Icons.check_circle_outline,
            title: 'Status',
            subtitle:
                auth?.isAuthenticated == true ? 'Connected' : 'Disconnected',
            trailing: Icon(
              Icons.circle,
              size: 12,
              color: auth?.isAuthenticated == true
                  ? AppTheme.available
                  : AppTheme.error,
            ),
          ),

          const SizedBox(height: 16),

          // Account
          const _SectionHeader(title: 'Account'),
          _SettingsTile(
            icon: Icons.person_outline,
            title: user?.username ?? 'Unknown',
            subtitle: user?.isAdmin == true ? 'Administrator' : 'User',
          ),
          if (user?.canUsePassword == true)
            _SettingsTile(
              icon: Icons.lock_outline,
              title: 'Password',
              subtitle: user?.hasPassword == null
                  ? 'Set a password for sign-in & MCP'
                  : (user!.hasPassword!
                      ? 'Change your sign-in password'
                      : 'Add a password for sign-in & MCP'),
              onTap: () => context.push('/settings/password'),
            ),
          if (user?.hasPermission('ai:chat') == true)
            _SettingsTile(
              icon: Icons.auto_awesome_outlined,
              title: 'AI Access',
              subtitle: _aiAccessSubtitle(aiSettings),
              onTap: () => context.push('/settings/ai'),
            ),

          const SizedBox(height: 16),

          // Modules (dynamic, instance-based)
          const _SectionHeader(title: 'Modules'),
          if (instances.isEmpty)
            const _SettingsTile(
              icon: Icons.info_outline,
              title: 'No instances configured',
              subtitle: 'Add a Radarr or Sonarr instance to get started',
            ),
          ...instances.map((inst) => _SettingsTile(
                icon: _serviceIcon(inst.serviceType),
                title: inst.name,
                subtitle:
                    '${_serviceLabel(inst.serviceType)}${inst.isDefault ? ' (Default)' : ''}',
                trailing: const Icon(
                  Icons.circle,
                  size: 12,
                  color: AppTheme.available,
                ),
                onTap: user?.isAdmin == true
                    ? () => context.push(
                          '/settings/instance/${inst.id}',
                          extra: {
                            'service_type': inst.serviceType,
                            'name': inst.name,
                            'is_default': inst.isDefault,
                          },
                        )
                    : null,
              )),
          _SettingsTile(
            icon: Icons.smart_toy_outlined,
            title: 'AI Assistant',
            subtitle: aiAvailable ? 'Available' : 'Not configured',
            trailing: Icon(
              Icons.circle,
              size: 12,
              color: aiAvailable ? AppTheme.available : AppTheme.unavailable,
            ),
          ),
          if (user?.isAdmin == true)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              child: OutlinedButton.icon(
                onPressed: () async {
                  final result =
                      await context.push<bool>('/settings/instance/new');
                  if (result != true || !context.mounted) return;
                  ScaffoldMessenger.of(context).showSnackBar(
                    const SnackBar(
                        content: Text(
                            'Instance added. Restart the app to see changes.')),
                  );
                },
                icon: const Icon(Icons.add),
                label: const Text('Add Instance'),
              ),
            ),

          if (user?.canUsePasskey == true)
            _SettingsTile(
              icon: Icons.fingerprint,
              title: 'Passkeys',
              subtitle: 'Manage passkey sign-in methods',
              onTap: () => context.push('/settings/passkeys'),
            ),

          // Admin section
          if (user?.isAdmin == true) ...[
            const SizedBox(height: 16),
            const _SectionHeader(title: 'Admin'),
            _SettingsTile(
              icon: Icons.checklist_outlined,
              title: 'Setup Checklist',
              subtitle: setupStatus == null
                  ? 'See which features are configured'
                  : '${setupStatus.configured} of ${setupStatus.total} features configured',
              onTap: () => context.push('/setup'),
            ),
            _SettingsTile(
              icon: Icons.key_outlined,
              title: 'Providers & Credentials',
              subtitle: 'Included AI, TMDB, and Trakt',
              onTap: () => context.push('/settings/credentials'),
            ),
            _SettingsTile(
              icon: Icons.handyman_outlined,
              title: 'AI Tools',
              subtitle: 'Enable or disable assistant tools',
              onTap: () => context.push('/settings/ai-tools'),
            ),
            _SettingsTile(
              icon: Icons.tune,
              title: 'Request Settings',
              subtitle: 'Approval, season, and quality defaults',
              onTap: () => context.push('/settings/request-settings'),
            ),
            _SettingsTile(
              icon: Icons.auto_fix_high_outlined,
              title: 'AI Remediation',
              subtitle: 'Problem reporting and auto-fix assistant',
              onTap: () => context.push('/settings/ai-remediation'),
            ),
            _SettingsTile(
              icon: Icons.people_outline,
              title: 'Users',
              subtitle: 'Manage accounts, roles, and invites',
              onTap: () => context.push('/settings/users'),
            ),
            _SettingsTile(
              icon: Icons.play_circle_outline,
              title: 'Plex Invites',
              subtitle: 'Link Plex for one-tap and automatic invites',
              onTap: () => context.push('/settings/plex'),
            ),
            _SettingsTile(
              icon: Icons.link,
              title: 'Generate Connect Link',
              subtitle: 'Create a link to invite a new user',
              onTap: () => _showGenerateConnectLinkDialog(context),
            ),
            _SettingsTile(
              icon: Icons.devices,
              title: 'Connected Devices',
              subtitle: 'Manage all connected devices',
              onTap: () => context.push('/settings/devices'),
            ),
            _SettingsTile(
              icon: Icons.open_in_new,
              title: 'Update Portal',
              subtitle: (updateStatus?.managementUrl.isNotEmpty ?? false)
                  ? updateStatus!.managementUrl
                  : 'Link your container manager for the update banner',
              onTap: () => _showManagementUrlDialog(
                context,
                updateStatus?.managementUrl ?? '',
              ),
            ),
          ],

          if (user?.isAdmin == true) ...[
            const SizedBox(height: 16),
            const _SectionHeader(title: 'Needs attention menu'),
            const AttentionMenuVisibilitySwitch(
              item: AttentionMenuItem.approvals,
            ),
            const AttentionMenuVisibilitySwitch(
              item: AttentionMenuItem.issues,
            ),
            const AttentionMenuVisibilitySwitch(
              item: AttentionMenuItem.agentFixes,
            ),
          ],

          const SizedBox(height: 16),

          // Notifications
          const _SectionHeader(title: 'Notifications'),
          _SettingsTile(
            icon: Icons.notifications_outlined,
            title: 'Notification Preferences',
            subtitle: 'Choose which push notifications you receive',
            onTap: () => context.push('/settings/notifications'),
          ),
          SwitchListTile(
            value: ref.watch(requestNotificationsEnabledProvider),
            onChanged: (v) =>
                ref.read(requestNotificationsEnabledProvider.notifier).set(v),
            activeThumbColor: AppTheme.accent,
            secondary: const Icon(Icons.notifications_active_outlined,
                color: AppTheme.textSecondary),
            title: const Text('Request updates',
                style: TextStyle(
                    color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
            subtitle: const Text(
                'Show an in-app banner when a request is approved or denied',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
          ),

          const SizedBox(height: 16),

          // Guides
          const _SectionHeader(title: 'Guides'),
          if (ref.watch(plexGuideEnabledProvider))
            _SettingsTile(
              icon: Icons.play_circle_outline,
              title: 'Watch on Plex',
              subtitle: 'Install Plex and start watching your requests',
              onTap: () => context.push('/plex-guide'),
            ),
          SwitchListTile(
            value: ref.watch(plexGuideEnabledProvider),
            onChanged: (v) =>
                ref.read(plexGuideEnabledProvider.notifier).set(v),
            activeThumbColor: AppTheme.accent,
            secondary: const Icon(Icons.visibility_outlined,
                color: AppTheme.textSecondary),
            title: const Text('Show Plex guide',
                style: TextStyle(
                    color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
            subtitle: const Text(
                'Show the Watch on Plex guide in the menu and here',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
          ),

          const SizedBox(height: 16),

          // About
          const _SectionHeader(title: 'About'),
          _SettingsTile(
            icon: Icons.info_outline,
            title: 'Cantinarr',
            subtitle: 'Version $_appVersion',
            onTap: () => showModalBottomSheet(
              context: context,
              backgroundColor: Colors.transparent,
              builder: (_) => const AboutSheet(),
            ),
          ),

          const SizedBox(height: 32),
        ],
      )),
    );
  }

  void _showGenerateConnectLinkDialog(BuildContext context) {
    final nameController = TextEditingController();
    String? generatedLink;
    bool isGenerating = false;

    showDialog(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('Generate Connect Link'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (generatedLink == null) ...[
                TextField(
                  controller: nameController,
                  decoration: const InputDecoration(
                    labelText: 'Name',
                    hintText: 'e.g. Mom, Dad, Roommate',
                    prefixIcon: Icon(Icons.person_outline),
                  ),
                  textCapitalization: TextCapitalization.words,
                  textInputAction: TextInputAction.done,
                ),
              ] else ...[
                const Text(
                  'Share this link:',
                  style: TextStyle(color: AppTheme.textSecondary),
                ),
                const SizedBox(height: 12),
                Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: AppTheme.accent.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: SelectableText(
                    generatedLink!,
                    style: const TextStyle(fontSize: 12),
                  ),
                ),
              ],
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(),
              child: Text(generatedLink != null ? 'Done' : 'Cancel'),
            ),
            if (generatedLink == null)
              ElevatedButton(
                onPressed: isGenerating
                    ? null
                    : () async {
                        final name = nameController.text.trim();
                        if (name.isEmpty) return;
                        setDialogState(() => isGenerating = true);
                        try {
                          final resp = await ref
                              .read(authProvider.notifier)
                              .generateConnectToken(name);
                          setDialogState(() {
                            generatedLink = resp.link;
                            isGenerating = false;
                          });
                        } catch (e) {
                          setDialogState(() => isGenerating = false);
                          if (dialogContext.mounted) {
                            ScaffoldMessenger.of(dialogContext).showSnackBar(
                              SnackBar(
                                  content: Text('Failed to generate link: $e')),
                            );
                          }
                        }
                      },
                child: isGenerating
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('Generate'),
              ),
            if (generatedLink != null)
              ElevatedButton.icon(
                onPressed: () {
                  Clipboard.setData(ClipboardData(text: generatedLink!));
                  ScaffoldMessenger.of(dialogContext).showSnackBar(
                    const SnackBar(content: Text('Link copied!')),
                  );
                },
                icon: const Icon(Icons.copy, size: 18),
                label: const Text('Copy'),
              ),
          ],
        ),
      ),
    );
  }

  void _showManagementUrlDialog(BuildContext context, String current) {
    final controller = TextEditingController(text: current);
    bool saving = false;

    showDialog(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('Update Portal'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Text(
                'Optional. When set, the "update available" banner links here so '
                'you can apply the update in your own container manager (e.g. an '
                'Unraid Docker page or Portainer). Leave blank to clear.',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: controller,
                decoration: const InputDecoration(
                  labelText: 'Portal URL',
                  hintText: 'http://tower.local/Docker',
                  prefixIcon: Icon(Icons.open_in_new),
                ),
                keyboardType: TextInputType.url,
                autocorrect: false,
                textInputAction: TextInputAction.done,
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(),
              child: const Text('Cancel'),
            ),
            ElevatedButton(
              onPressed: saving
                  ? null
                  : () async {
                      setDialogState(() => saving = true);
                      try {
                        await ref
                            .read(updateStatusProvider.notifier)
                            .setManagementUrl(controller.text.trim());
                        if (dialogContext.mounted) {
                          Navigator.of(dialogContext).pop();
                        }
                      } catch (e) {
                        setDialogState(() => saving = false);
                        if (dialogContext.mounted) {
                          ScaffoldMessenger.of(dialogContext).showSnackBar(
                            SnackBar(content: Text('Failed to save: $e')),
                          );
                        }
                      }
                    },
              child: saving
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Text('Save'),
            ),
          ],
        ),
      ),
    );
  }
}

IconData _serviceIcon(String serviceType) {
  switch (serviceType) {
    case 'radarr':
      return Icons.movie_outlined;
    case 'sonarr':
      return Icons.tv_outlined;
    case 'chaptarr':
      return Icons.menu_book;
    case 'sabnzbd':
    case 'qbittorrent':
    case 'nzbget':
    case 'transmission':
      return Icons.download_outlined;
    case 'tautulli':
      return Icons.monitor_heart_outlined;
    default:
      return Icons.dns_outlined;
  }
}

String _serviceLabel(String serviceType) {
  switch (serviceType) {
    case 'radarr':
      return 'Radarr';
    case 'sonarr':
      return 'Sonarr';
    case 'chaptarr':
      return 'Chaptarr';
    case 'sabnzbd':
      return 'SABnzbd';
    case 'qbittorrent':
      return 'qBittorrent';
    case 'nzbget':
      return 'NZBGet';
    case 'transmission':
      return 'Transmission';
    case 'tautulli':
      return 'Tautulli';
    default:
      return serviceType;
  }
}

String _aiAccessSubtitle(AiSettings? settings) {
  if (settings == null) return 'Choose personal or included AI';
  final effective = settings.effective;
  final provider = effective.provider.isEmpty
      ? ''
      : settings.providerLabel(effective.provider);
  if (effective.source == AiAccessSource.personal) {
    return effective.available
        ? 'Personal · $provider'
        : 'Personal AI needs attention';
  }
  if (effective.source == AiAccessSource.shared) {
    return effective.available
        ? 'Included · $provider'
        : 'Included AI unavailable';
  }
  if (settings.shared.granted) {
    return 'Included access needs server setup';
  }
  return 'Add a personal provider';
}

class _SectionHeader extends StatelessWidget {
  final String title;
  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 14, 20, 8),
      child: Row(
        children: [
          Container(
            width: 18,
            height: 2,
            decoration: BoxDecoration(
              color: AppTheme.accent,
              borderRadius: BorderRadius.circular(99),
            ),
          ),
          const SizedBox(width: 9),
          Text(
            title.toUpperCase(),
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 11,
              fontWeight: FontWeight.w800,
              letterSpacing: 1.25,
            ),
          ),
        ],
      ),
    );
  }
}

class _SettingsTile extends StatelessWidget {
  final IconData icon;
  final String title;
  final String subtitle;
  final VoidCallback? onTap;
  final Widget? trailing;

  const _SettingsTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    this.onTap,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 3),
      child: Material(
        color: AppTheme.surfaceVariant.withValues(alpha: 0.72),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
          side: const BorderSide(color: AppTheme.border),
        ),
        clipBehavior: Clip.antiAlias,
        child: ListTile(
          leading: Container(
            width: 38,
            height: 38,
            decoration: BoxDecoration(
              color: AppTheme.surfaceRaised,
              borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
              border: Border.all(color: AppTheme.border),
            ),
            child: Icon(icon, color: AppTheme.textSecondary, size: 20),
          ),
          title: Text(
            title,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontWeight: FontWeight.w600,
            ),
          ),
          subtitle: Text(
            subtitle,
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 13,
            ),
          ),
          trailing: trailing ??
              (onTap != null
                  ? const Icon(
                      Icons.arrow_forward_ios_rounded,
                      size: 15,
                      color: AppTheme.textMuted,
                    )
                  : null),
          onTap: onTap,
        ),
      ),
    );
  }
}
