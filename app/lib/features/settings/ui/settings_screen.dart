import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:package_info_plus/package_info_plus.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
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
  }

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);
    final auth = authState.valueOrNull;
    final connection = auth?.connection;
    final user = auth?.user;
    final instances = connection?.instances ?? [];

    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        padding: const EdgeInsets.symmetric(vertical: 8),
        children: [
          // Server connection
          _SectionHeader(title: 'Server'),
          _SettingsTile(
            icon: Icons.dns_outlined,
            title: connection?.serverName ?? 'Cantinarr',
            subtitle: connection?.serverUrl ?? 'Not connected',
          ),
          _SettingsTile(
            icon: Icons.check_circle_outline,
            title: 'Status',
            subtitle: auth?.isAuthenticated == true
                ? 'Connected'
                : 'Disconnected',
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
          _SectionHeader(title: 'Account'),
          _SettingsTile(
            icon: Icons.person_outline,
            title: user?.username ?? 'Unknown',
            subtitle: user?.isAdmin == true ? 'Administrator' : 'User',
          ),

          const SizedBox(height: 16),

          // Modules (dynamic, instance-based)
          _SectionHeader(title: 'Modules'),
          if (instances.isEmpty)
            _SettingsTile(
              icon: Icons.info_outline,
              title: 'No instances configured',
              subtitle: 'Add a Radarr or Sonarr instance to get started',
            ),
          ...instances.map((inst) => _SettingsTile(
                icon: inst.serviceType == 'radarr'
                    ? Icons.movie_outlined
                    : Icons.tv_outlined,
                title: inst.name,
                subtitle:
                    '${inst.serviceType == 'radarr' ? 'Radarr' : 'Sonarr'}${inst.isDefault ? ' (Default)' : ''}',
                trailing: Icon(
                  Icons.circle,
                  size: 12,
                  color: AppTheme.available,
                ),
                onTap: user?.isAdmin == true
                    ? () => context.push('/settings/instance/${inst.id}')
                    : null,
              )),
          _SettingsTile(
            icon: Icons.smart_toy_outlined,
            title: 'AI Assistant',
            subtitle: connection?.services.ai == true
                ? 'Available'
                : 'Not configured',
            trailing: Icon(
              Icons.circle,
              size: 12,
              color: connection?.services.ai == true
                  ? AppTheme.available
                  : AppTheme.unavailable,
            ),
          ),
          if (user?.isAdmin == true)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
              child: OutlinedButton.icon(
                onPressed: () async {
                  final result =
                      await context.push<bool>('/settings/instance/new');
                  if (result == true && mounted) {
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(
                          content: Text(
                              'Instance added. Restart the app to see changes.')),
                    );
                  }
                },
                icon: const Icon(Icons.add),
                label: const Text('Add Instance'),
              ),
            ),

          _SettingsTile(
            icon: Icons.fingerprint,
            title: 'Passkeys',
            subtitle: 'Manage passkey sign-in methods',
            onTap: () => context.push('/settings/passkeys'),
          ),

          // Admin section
          if (user?.isAdmin == true) ...[
            const SizedBox(height: 16),
            _SectionHeader(title: 'Admin'),
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
          ],

          const SizedBox(height: 16),

          // Guides
          _SectionHeader(title: 'Guides'),
          _SettingsTile(
            icon: Icons.play_circle_outline,
            title: 'Plex Setup Guide',
            subtitle: 'How to connect Plex to your library',
            onTap: () => context.push('/plex-guide'),
          ),

          const SizedBox(height: 16),

          // About
          _SectionHeader(title: 'About'),
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
      ),
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
                                  content:
                                      Text('Failed to generate link: $e')),
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
}

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
    return ListTile(
      leading: Icon(icon, color: AppTheme.textSecondary),
      title: Text(title,
          style: const TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
      subtitle: Text(subtitle,
          style:
              const TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
      trailing: trailing ??
          (onTap != null
              ? const Icon(Icons.chevron_right,
                  color: AppTheme.textSecondary)
              : null),
      onTap: onTap,
    );
  }
}
