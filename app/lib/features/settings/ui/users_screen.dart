import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../ai_assistant/data/ai_settings_service.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../../notifications/push_service.dart';
import '../data/plex_admin_service.dart';
import '../data/credentials_service.dart';
import '../logic/plex_invites_provider.dart';

/// Admin screen for managing user accounts: change roles, remove users, and
/// see who still has an outstanding connect-link invite.
class UsersScreen extends ConsumerStatefulWidget {
  const UsersScreen({super.key});

  @override
  ConsumerState<UsersScreen> createState() => _UsersScreenState();
}

class _UsersScreenState extends ConsumerState<UsersScreen> {
  List<UserSummary>? _users;
  bool _isLoading = true;
  String? _error;
  String _sharedAiProvider = '';

  @override
  void initState() {
    super.initState();
    _loadUsers();
  }

  Future<void> _loadUsers() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final users = await ref.read(authProvider.notifier).listUsers();
      try {
        final credentials = await CredentialsService(
          backendDio: ref.read(backendClientProvider),
        ).getStatus();
        _sharedAiProvider = credentials.ai.provider;
      } catch (_) {
        // User management remains usable if provider status is temporarily
        // unavailable. The confirmation falls back to a generic quota warning.
      }
      // Keep the drawer's "Plex invites" badge in step with what this
      // screen just learned (e.g. an invite sent here clears the count).
      ref.read(plexInvitesWaitingProvider.notifier).refresh();
      setState(() {
        _users = users;
        _isLoading = false;
      });
    } catch (e) {
      setState(() {
        _error = 'Failed to load users';
        _isLoading = false;
      });
    }
  }

  /// One-tap invite through the linked Plex account: the server shares the
  /// configured libraries with the user's email and stamps the invite.
  Future<void> _sendPlexInvite(UserSummary user) async {
    try {
      final status =
          await ref.read(plexAdminServiceProvider).inviteUser(user.id);
      await _loadUsers();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(status == 'already_shared'
            ? '${user.plexEmail} already has access on Plex'
            : 'Plex invite sent to ${user.plexEmail}'),
      ));
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
          content:
              Text('Plex invite failed — check the Plex Invites settings')));
    }
  }

  /// Copies the user's Plex email and opens Plex's Manage Library Access page,
  /// where the admin pastes it into Grant Library Access. The fallback when no
  /// Plex account is linked (Plex offers no way to prefill the invite).
  Future<void> _inviteInPlex(UserSummary user) async {
    await Clipboard.setData(ClipboardData(text: user.plexEmail));
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(
            'Copied ${user.plexEmail} — paste it into Grant Library Access',
          ),
        ),
      );
    }
    await launchUrl(
      Uri.parse(
          'https://app.plex.tv/desktop/#!/settings/manage-library-access'),
      mode: LaunchMode.externalApplication,
    );
  }

  Future<void> _changeRole(UserSummary user, String newRole) async {
    if (newRole == user.role) return;
    try {
      await ref.read(authProvider.notifier).updateUserRole(user.id, newRole);
      await _loadUsers();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              newRole == 'admin'
                  ? '${user.username} is now an admin'
                  : '${user.username} is now a user',
            ),
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(_friendlyError(e, 'Failed to change role'))),
        );
      }
    }
  }

  Future<void> _deleteUser(UserSummary user) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Remove User'),
        content: Text(
          'Remove "${user.username}"? This deletes their account, devices, '
          'and any pending invites. This cannot be undone.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(backgroundColor: AppTheme.error),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );

    if (confirmed != true) return;

    try {
      await ref.read(authProvider.notifier).deleteUser(user.id);
      await _loadUsers();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Removed ${user.username}')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(_friendlyError(e, 'Failed to remove user'))),
        );
      }
    }
  }

  /// Issue a fresh connect link for a user who hasn't connected a device yet.
  ///
  /// Reuses the connect-token endpoint, which finds the existing account by
  /// username and attaches a new token — so a user stuck in invited limbo
  /// (lost or expired link) can be re-invited without losing their account.
  Future<void> _resendInvite(UserSummary user) async {
    String? link;
    try {
      final resp = await ref.read(authProvider.notifier).generateConnectToken(
            user.username,
          );
      link = resp.link;
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(_friendlyError(e, 'Failed to create link'))),
        );
      }
      return;
    }

    await _loadUsers();
    if (!mounted) return;

    final newLink = link;
    await showDialog<void>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text('Invite link for ${user.username}'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              'Share this link with them. It replaces any previous link and '
              'expires in 7 days.',
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
                newLink,
                style: const TextStyle(fontSize: 12),
              ),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(),
            child: const Text('Done'),
          ),
          ElevatedButton.icon(
            onPressed: () {
              Clipboard.setData(ClipboardData(text: newLink));
              ScaffoldMessenger.of(dialogContext).showSnackBar(
                const SnackBar(content: Text('Link copied!')),
              );
            },
            icon: const Icon(Icons.copy, size: 18),
            label: const Text('Copy'),
          ),
        ],
      ),
    );
  }

  /// Send a test push to a user's devices and report the real outcome — how
  /// many devices are registered and whether Apple accepted the push. The
  /// self-only test on the notifications screen can't reach another account, so
  /// this is how an admin verifies a specific user's delivery.
  Future<void> _sendTestPush(UserSummary user) async {
    try {
      final result =
          await ref.read(pushServiceProvider).sendTestToUser(user.id);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(describePushTest(result, username: user.username)),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e, 'Failed to send test push'))),
      );
    }
  }

  /// Enable or disable a user's password / passkey sign-in. Disabling is a real
  /// revoke (clears the password / deletes passkeys), so confirm it first.
  Future<void> _setAuthMethods(
    UserSummary user, {
    bool? passwordEnabled,
    bool? passkeyEnabled,
  }) async {
    if (passwordEnabled == false || passkeyEnabled == false) {
      final isPassword = passwordEnabled == false;
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text(isPassword ? 'Disable password?' : 'Disable passkeys?'),
          content: Text(
            isPassword
                ? "This clears ${user.username}'s password. They can set a new "
                    'one only if you re-enable it.'
                : "This deletes ${user.username}'s passkeys. They'll need to "
                    'register again if you re-enable them.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('Cancel'),
            ),
            ElevatedButton(
              style: ElevatedButton.styleFrom(backgroundColor: AppTheme.error),
              onPressed: () => Navigator.pop(context, true),
              child: const Text('Disable'),
            ),
          ],
        ),
      );
      if (confirmed != true) return;
    }

    try {
      await ref.read(authProvider.notifier).updateUserAuthMethods(
            user.id,
            passwordEnabled: passwordEnabled,
            passkeyEnabled: passkeyEnabled,
          );
      await _loadUsers();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text("Updated ${user.username}'s sign-in methods")),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content:
                Text(_friendlyError(e, 'Failed to update sign-in methods')),
          ),
        );
      }
    }
  }

  Future<void> _setSharedAiAccess(UserSummary user, bool enabled) async {
    if (enabled) {
      // Re-read at the decision boundary: another admin/device may have
      // switched the included provider since this screen loaded. A failed
      // refresh deliberately becomes "unknown" so the stronger combined
      // warning is shown instead of trusting a stale API-key snapshot.
      var currentProvider = '';
      try {
        final credentials = await CredentialsService(
          backendDio: ref.read(backendClientProvider),
        ).getStatus();
        currentProvider = credentials.ai.provider;
        if (mounted) {
          setState(() => _sharedAiProvider = currentProvider);
        }
      } catch (_) {
        if (mounted) {
          setState(() => _sharedAiProvider = '');
        }
      }
      if (!mounted) return;
      final codex = currentProvider == 'codex';
      final providerUnknown = currentProvider.isEmpty;
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (dialogContext) => AlertDialog(
          title: Text('Include AI access for ${user.username}?'),
          content: Text(
            codex
                ? 'Prompts and tool context will use the shared OpenAI OAuth '
                    'account. All enabled users consume the same Codex '
                    'allowance, and activity is attributable to that account. '
                    'Any subscription or usage costs remain with it. ChatGPT '
                    'accounts are intended for one person—only enable this for '
                    'people or devices you control.'
                : providerUnknown
                    ? 'Cantinarr could not confirm which shared provider is '
                        'selected. If it is OpenAI OAuth, prompts and tool context '
                        'will use one shared account and Codex allowance, '
                        'activity is attributable to that account, and any '
                        'subscription or usage costs remain with it. ChatGPT '
                        'accounts are intended for one person—only enable this '
                        'for people or devices you control. If it uses an API '
                        'key, requests count against that provider\'s paid quota '
                        'and may create charges.'
                    : 'This user can send prompts and tool context through the '
                        'server AI provider. Requests count against its paid quota '
                        'and may create provider charges. Their selected personal '
                        'provider still takes priority.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(false),
              child: const Text('Cancel'),
            ),
            ElevatedButton(
              onPressed: () => Navigator.of(dialogContext).pop(true),
              child: const Text('Include AI access'),
            ),
          ],
        ),
      );
      if (confirmed != true) return;
    }

    try {
      await ref
          .read(authProvider.notifier)
          .updateUserAiAccess(user.id, enabled);
      final currentUserID = ref.read(authProvider).valueOrNull?.user?.id;
      if (currentUserID == user.id) {
        ref.invalidate(aiSettingsProvider);
        try {
          await ref.read(authProvider.notifier).refreshConfig();
        } catch (_) {
          // The grant is already saved. Config refresh retries on resume, and
          // the AI settings screen re-fetches its authoritative source now.
        }
      }
      await _loadUsers();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(enabled
              ? 'Included AI enabled for ${user.username}'
              : 'Included AI removed for ${user.username}'),
        ),
      );
    } catch (error) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(_friendlyError(error, 'Failed to update AI access')),
        ),
      );
    }
  }

  String _friendlyError(Object e, String fallback) {
    final msg = e.toString();
    // Surface the backend's error message when present.
    final match = RegExp(r'"error":"([^"]+)"').firstMatch(msg);
    return match != null ? match.group(1)! : fallback;
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Users')),
      body: CenteredContent(child: _buildBody()),
    );
  }

  Widget _buildBody() {
    if (_isLoading) {
      return const Center(child: CircularProgressIndicator());
    }

    if (_error != null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(_error!, style: const TextStyle(color: AppTheme.error)),
            const SizedBox(height: 16),
            ElevatedButton(onPressed: _loadUsers, child: const Text('Retry')),
          ],
        ),
      );
    }

    final users = _users ?? [];
    if (users.isEmpty) {
      return const Center(
        child: Text(
          'No users yet',
          style: TextStyle(color: AppTheme.textSecondary),
        ),
      );
    }

    final currentUserId = ref.read(authProvider).valueOrNull?.user?.id;
    final plexConfigured =
        ref.watch(plexInviteConfiguredProvider).valueOrNull ?? false;

    return RefreshIndicator(
      onRefresh: _loadUsers,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: users.length,
        separatorBuilder: (_, __) =>
            const Divider(height: 1, color: AppTheme.border),
        itemBuilder: (context, index) {
          final user = users[index];
          return _UserTile(
            user: user,
            isSelf: user.id == currentUserId,
            plexInviteConfigured: plexConfigured,
            onChangeRole: (role) => _changeRole(user, role),
            onDelete: () => _deleteUser(user),
            onResendInvite: () => _resendInvite(user),
            onSendTestPush: () => _sendTestPush(user),
            onSendPlexInvite: () => _sendPlexInvite(user),
            onInviteInPlex: () => _inviteInPlex(user),
            onRequestSettings: () => context.push(
              '/settings/users/${user.id}/request-settings',
              extra: user.username,
            ),
            onSetAuthMethods: ({bool? passwordEnabled, bool? passkeyEnabled}) =>
                _setAuthMethods(
              user,
              passwordEnabled: passwordEnabled,
              passkeyEnabled: passkeyEnabled,
            ),
            onSetSharedAiAccess: (enabled) => _setSharedAiAccess(user, enabled),
            sharedAiProvider: _sharedAiProvider,
          );
        },
      ),
    );
  }
}

class _UserTile extends StatelessWidget {
  const _UserTile({
    required this.user,
    required this.isSelf,
    required this.plexInviteConfigured,
    required this.onChangeRole,
    required this.onDelete,
    required this.onResendInvite,
    required this.onSendTestPush,
    required this.onSendPlexInvite,
    required this.onInviteInPlex,
    required this.onSetAuthMethods,
    required this.onRequestSettings,
    required this.onSetSharedAiAccess,
    required this.sharedAiProvider,
  });

  final UserSummary user;
  final bool isSelf;
  final bool plexInviteConfigured;
  final ValueChanged<String> onChangeRole;
  final VoidCallback onDelete;
  final VoidCallback onResendInvite;
  final VoidCallback onSendTestPush;
  final VoidCallback onSendPlexInvite;
  final VoidCallback onInviteInPlex;
  final void Function({bool? passwordEnabled, bool? passkeyEnabled})
      onSetAuthMethods;
  final VoidCallback onRequestSettings;
  final ValueChanged<bool> onSetSharedAiAccess;
  final String sharedAiProvider;

  /// A user who has never connected a device is stuck in "invited limbo":
  /// either their invite is still pending or the link was lost/expired.
  bool get _needsInvite => user.deviceCount == 0;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: CircleAvatar(
        backgroundColor: user.isAdmin
            ? AppTheme.accent.withValues(alpha: 0.2)
            : AppTheme.surfaceVariant,
        child: Icon(
          user.isAdmin ? Icons.admin_panel_settings : Icons.person,
          color: user.isAdmin ? AppTheme.accent : AppTheme.textSecondary,
          size: 20,
        ),
      ),
      title: Row(
        children: [
          Flexible(
            child: Text(
              user.username,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontWeight: FontWeight.w600,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (isSelf) ...[
            const SizedBox(width: 8),
            const _Tag(label: 'You', color: AppTheme.accent),
          ],
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Wrap(
          spacing: 6,
          runSpacing: 4,
          children: [
            _Tag(
              label: user.isAdmin ? 'Admin' : 'User',
              color: user.isAdmin ? AppTheme.accent : AppTheme.textSecondary,
            ),
            if (user.hasPendingInvite)
              const _Tag(label: 'Invited', color: AppTheme.requested)
            else if (_needsInvite)
              const _Tag(label: 'Invite expired', color: AppTheme.unavailable),
            _Tag(
              label: user.deviceCount == 1
                  ? '1 device'
                  : '${user.deviceCount} devices',
              color: user.deviceCount > 0
                  ? AppTheme.available
                  : AppTheme.unavailable,
            ),
            if (user.passwordEnabled)
              const _Tag(label: 'Password', color: AppTheme.textSecondary),
            if (user.passkeyEnabled)
              const _Tag(label: 'Passkey', color: AppTheme.textSecondary),
            if (user.sharedAiEnabled)
              const _Tag(label: 'AI included', color: AppTheme.signal),
            if (user.plexEmail.isNotEmpty)
              _Tag(label: user.plexEmail, color: AppTheme.textSecondary),
            if (user.plexInvitedAt != null)
              const _Tag(label: 'Plex invite sent', color: AppTheme.available)
            else if (user.plexEmail.isNotEmpty)
              const _Tag(label: 'Needs Plex invite', color: AppTheme.requested),
          ],
        ),
      ),
      trailing: _buildMenu(context),
    );
  }

  Widget _buildMenu(BuildContext context) {
    return PopupMenuButton<String>(
      icon: const Icon(Icons.more_vert, color: AppTheme.textSecondary),
      onSelected: (value) {
        switch (value) {
          case 'make_admin':
            onChangeRole('admin');
            break;
          case 'make_user':
            onChangeRole('user');
            break;
          case 'resend_invite':
            onResendInvite();
            break;
          case 'test_push':
            onSendTestPush();
            break;
          case 'send_plex_invite':
            onSendPlexInvite();
            break;
          case 'invite_in_plex':
            onInviteInPlex();
            break;
          case 'request_settings':
            onRequestSettings();
            break;
          case 'toggle_shared_ai':
            onSetSharedAiAccess(!user.sharedAiEnabled);
            break;
          case 'enable_password':
            onSetAuthMethods(passwordEnabled: true);
            break;
          case 'disable_password':
            onSetAuthMethods(passwordEnabled: false);
            break;
          case 'enable_passkey':
            onSetAuthMethods(passkeyEnabled: true);
            break;
          case 'disable_passkey':
            onSetAuthMethods(passkeyEnabled: false);
            break;
          case 'delete':
            onDelete();
            break;
        }
      },
      itemBuilder: (context) => [
        const PopupMenuItem(
          value: 'request_settings',
          child: ListTile(
            leading: Icon(Icons.tune),
            title: Text('User settings…'),
            contentPadding: EdgeInsets.zero,
          ),
        ),
        PopupMenuItem(
          value: 'toggle_shared_ai',
          child: ListTile(
            leading: const Icon(Icons.auto_awesome_outlined),
            title: const Text('Included AI access'),
            subtitle: Text(
              sharedAiProvider == 'codex'
                  ? 'Shared OpenAI OAuth allowance'
                  : sharedAiProvider.isEmpty
                      ? 'Provider status unavailable'
                      : 'Server provider quota',
            ),
            trailing: IgnorePointer(
              child: Switch(
                value: user.sharedAiEnabled,
                onChanged: (_) {},
                activeThumbColor: AppTheme.accent,
              ),
            ),
            contentPadding: EdgeInsets.zero,
          ),
        ),
        // A connect link works for any user: re-invite one stuck in invited
        // limbo, re-auth one who lost their session, or authorize a new device
        // for one who already has one (find-or-create reuses the account).
        if (!isSelf)
          PopupMenuItem(
            value: 'resend_invite',
            child: ListTile(
              leading: const Icon(Icons.link),
              title: Text(
                user.hasPendingInvite
                    ? 'New invite link'
                    : (_needsInvite ? 'Re-invite' : 'Issue device link'),
              ),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        // The user shared their Plex email. With a linked Plex account the
        // invite is one tap; otherwise fall back to copy-email-and-open-Plex.
        if (user.plexEmail.isNotEmpty && plexInviteConfigured)
          PopupMenuItem(
            value: 'send_plex_invite',
            child: ListTile(
              leading: const Icon(Icons.send_outlined),
              title: Text(user.plexInvitedAt != null
                  ? 'Resend Plex invite'
                  : 'Send Plex invite'),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (user.plexEmail.isNotEmpty && !plexInviteConfigured)
          const PopupMenuItem(
            value: 'invite_in_plex',
            child: ListTile(
              leading: Icon(Icons.play_circle_outline),
              title: Text('Invite in Plex…'),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (!isSelf)
          const PopupMenuItem(
            value: 'test_push',
            child: ListTile(
              leading: Icon(Icons.notifications_active_outlined),
              title: Text('Send test push'),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (!user.isAdmin)
          const PopupMenuItem(
            value: 'make_admin',
            child: ListTile(
              leading: Icon(Icons.arrow_upward),
              title: Text('Make admin'),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (user.isAdmin && !isSelf)
          const PopupMenuItem(
            value: 'make_user',
            child: ListTile(
              leading: Icon(Icons.arrow_downward),
              title: Text('Make user'),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        // Admins always keep both methods, so toggles are only for other users.
        if (!user.isAdmin)
          PopupMenuItem(
            value:
                user.passwordEnabled ? 'disable_password' : 'enable_password',
            child: ListTile(
              leading: Icon(
                  user.passwordEnabled ? Icons.lock_outline : Icons.lock_open),
              title: Text(user.passwordEnabled
                  ? 'Disable password'
                  : 'Enable password'),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (!user.isAdmin)
          PopupMenuItem(
            value: user.passkeyEnabled ? 'disable_passkey' : 'enable_passkey',
            child: ListTile(
              leading: const Icon(Icons.fingerprint),
              title: Text(
                  user.passkeyEnabled ? 'Disable passkeys' : 'Enable passkeys'),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (!isSelf)
          const PopupMenuItem(
            value: 'delete',
            child: ListTile(
              leading: Icon(Icons.delete_outline, color: AppTheme.error),
              title: Text('Remove', style: TextStyle(color: AppTheme.error)),
              contentPadding: EdgeInsets.zero,
            ),
          ),
      ],
    );
  }
}

class _Tag extends StatelessWidget {
  const _Tag({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
