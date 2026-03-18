import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
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
  String? _generatedCode;
  bool _isGenerating = false;

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);
    final auth = authState.valueOrNull;
    final connection = auth?.connection;
    final user = auth?.user;

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

          // Services
          _SectionHeader(title: 'Services'),
          _SettingsTile(
            icon: Icons.movie_outlined,
            title: 'Radarr',
            subtitle: connection?.services.radarr == true
                ? 'Available'
                : 'Not configured',
            trailing: Icon(
              Icons.circle,
              size: 12,
              color: connection?.services.radarr == true
                  ? AppTheme.available
                  : AppTheme.unavailable,
            ),
          ),
          _SettingsTile(
            icon: Icons.tv_outlined,
            title: 'Sonarr',
            subtitle: connection?.services.sonarr == true
                ? 'Available'
                : 'Not configured',
            trailing: Icon(
              Icons.circle,
              size: 12,
              color: connection?.services.sonarr == true
                  ? AppTheme.available
                  : AppTheme.unavailable,
            ),
          ),
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

          // Admin section
          if (user?.isAdmin == true) ...[
            const SizedBox(height: 16),
            _SectionHeader(title: 'Admin'),
            _SettingsTile(
              icon: Icons.vpn_key_outlined,
              title: 'Generate Invite Code',
              subtitle: _generatedCode != null
                  ? 'Code: $_generatedCode (tap to copy)'
                  : 'Create a code to invite new users',
              onTap: _generatedCode != null
                  ? () {
                      Clipboard.setData(
                          ClipboardData(text: _generatedCode!));
                      ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(
                            content: Text('Invite code copied!')),
                      );
                    }
                  : _generateInviteCode,
              trailing: _isGenerating
                  ? const SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(
                          strokeWidth: 2, color: AppTheme.accent),
                    )
                  : null,
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
            subtitle: 'Version 1.0.0',
            onTap: () => showModalBottomSheet(
              context: context,
              backgroundColor: Colors.transparent,
              builder: (_) => const AboutSheet(),
            ),
          ),

          const SizedBox(height: 24),

          // Logout
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: OutlinedButton.icon(
              onPressed: () async {
                await ref.read(authProvider.notifier).logout();
                if (context.mounted) context.go('/login');
              },
              icon: const Icon(Icons.logout, color: AppTheme.error),
              label:
                  const Text('Sign Out', style: TextStyle(color: AppTheme.error)),
              style: OutlinedButton.styleFrom(
                side: const BorderSide(color: AppTheme.error),
                shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(12)),
                padding: const EdgeInsets.symmetric(vertical: 14),
              ),
            ),
          ),

          const SizedBox(height: 32),
        ],
      ),
    );
  }

  Future<void> _generateInviteCode() async {
    setState(() => _isGenerating = true);
    try {
      final code =
          await ref.read(authProvider.notifier).generateInviteCode();
      setState(() {
        _generatedCode = code;
        _isGenerating = false;
      });
    } catch (e) {
      setState(() => _isGenerating = false);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to generate code: $e')),
        );
      }
    }
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
