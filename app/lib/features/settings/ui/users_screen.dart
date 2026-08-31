import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../ai_assistant/data/ai_settings_service.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../../media_access/data/media_access_service.dart';
import '../../media_access/ui/media_server_import_sheet.dart';
import '../../media_access/ui/media_server_link_sheet.dart';
import '../../notifications/push_service.dart';
import '../data/credentials_service.dart';
import '../data/request_settings_service.dart';
import '../logic/plex_invites_provider.dart';

/// Admin screen for managing user accounts: invite new users, change roles,
/// remove users, and see who still has an outstanding connect-link invite.
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
  bool _sharedAiConfigured = true;

  // Media-server (Jellyfin) account rows, one per linked (user, server).
  // A failed read is said as such: an empty list would read as "nobody has
  // an account", which is a different answer.
  List<MediaServerAccountRow> _mediaAccounts = const [];
  bool _mediaAccountsFailed = false;

  /// The media servers this admin's config lists (admins see every
  /// instance), which is what the per-user tags and menu entries iterate.
  List<ServiceInstance> get _mediaServers =>
      ref.read(authProvider).valueOrNull?.connection?.mediaServerInstances ??
      const [];

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
        _sharedAiConfigured = credentials.ai.sharedConfigured;
      } catch (_) {
        // User management remains usable if provider status is temporarily
        // unavailable. The confirmation falls back to a generic quota warning.
      }
      var mediaAccounts = const <MediaServerAccountRow>[];
      var mediaAccountsFailed = false;
      if (_mediaServers.isNotEmpty) {
        try {
          mediaAccounts =
              await ref.read(mediaAccessServiceProvider).listAccounts();
        } catch (_) {
          mediaAccountsFailed = true;
        }
      }
      // Keep the drawer's "Plex invites" badge in step with what this
      // screen just learned (e.g. a grant given here clears the count).
      ref.read(plexInvitesWaitingProvider.notifier).refresh();
      setState(() {
        _users = users;
        _mediaAccounts = mediaAccounts;
        _mediaAccountsFailed = mediaAccountsFailed;
        _isLoading = false;
      });
    } catch (e) {
      setState(() {
        _error = 'Failed to load users';
        _isLoading = false;
      });
    }
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
    await _generateAndShowInviteLink(
      user.username,
      'Share this link with them. It signs one device into the app, '
      'once. It replaces any previous link and expires in 7 days.',
    );
  }

  /// Invite someone new: ask for a name, then mint their first connect link.
  /// The connect-token endpoint creates the account on the spot, so the new
  /// row is in the list by the time the link dialog shows.
  Future<void> _inviteNewUser() async {
    final nameController = TextEditingController();
    final name = await showDialog<String>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: const Text('Invite a new user'),
        content: TextField(
          controller: nameController,
          autofocus: true,
          decoration: const InputDecoration(
            labelText: 'Name',
            hintText: 'e.g. Mom, Dad, Roommate',
            prefixIcon: Icon(Icons.person_outline),
          ),
          textCapitalization: TextCapitalization.words,
          textInputAction: TextInputAction.done,
          onSubmitted: (value) {
            final trimmed = value.trim();
            if (trimmed.isEmpty) return;
            Navigator.of(dialogContext).pop(trimmed);
          },
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () {
              final trimmed = nameController.text.trim();
              if (trimmed.isEmpty) return;
              Navigator.of(dialogContext).pop(trimmed);
            },
            child: const Text('Create invite'),
          ),
        ],
      ),
    );
    if (name == null || name.isEmpty || !mounted) return;
    await _generateAndShowInviteLink(
      name,
      'Share this link with them. It signs one device into the app, '
      'once, and expires in 7 days.',
    );
  }

  Future<void> _generateAndShowInviteLink(
    String username,
    String description,
  ) async {
    String? link;
    var originSource = '';
    try {
      final resp = await ref.read(authProvider.notifier).generateConnectToken(
            username,
          );
      link = resp.link;
      originSource = resp.originSource;
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
        title: Text('Invite link for $username'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              description,
              style: const TextStyle(color: AppTheme.textSecondary),
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
            if (originSource == 'app') ...[
              const SizedBox(height: 12),
              const Text(
                'This link uses the address your app connects with. If they '
                'are not on your network, set External Address in Settings '
                'and issue a new link.',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 12),
              ),
            ],
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
  /// many devices are registered and whether the platform's push service
  /// (APNs/FCM) accepted it. The self-only test on the notifications screen
  /// can't reach another account, so this is how an admin verifies a specific
  /// user's delivery.
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
      var currentConfigured = true;
      try {
        final credentials = await CredentialsService(
          backendDio: ref.read(backendClientProvider),
        ).getStatus();
        currentProvider = credentials.ai.provider;
        currentConfigured = credentials.ai.sharedConfigured;
        if (mounted) {
          setState(() {
            _sharedAiProvider = currentProvider;
            _sharedAiConfigured = currentConfigured;
          });
        }
      } catch (_) {
        if (mounted) {
          setState(() => _sharedAiProvider = '');
        }
      }
      if (!mounted) return;
      final providerUnknown = currentProvider.isEmpty;
      // The server defaults the provider name even with nothing set up, so
      // the provider-specific warnings only apply when one actually exists.
      final unconfigured = !currentConfigured && !providerUnknown;
      final codex = currentProvider == 'codex' && !unconfigured;
      final grokOAuth = currentProvider == 'grok_oauth' && !unconfigured;
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (dialogContext) => AlertDialog(
          title: Text('Include AI access for ${user.username}?'),
          content: Text(
            unconfigured
                ? 'No shared AI provider is set up yet, so this only stages '
                    'access. Once an admin configures one under Providers & '
                    'Credentials, this user\'s prompts will run through it '
                    'and count against its quota or allowance.'
                : codex
                ? 'Prompts and tool context will use the shared OpenAI OAuth '
                    'account. All enabled users consume the same Codex '
                    'allowance, and activity is attributable to that account. '
                    'Any subscription or usage costs remain with it. ChatGPT '
                    'accounts are intended for one person—only enable this for '
                    'people or devices you control.'
                : grokOAuth
                ? 'Prompts and tool context will use the shared xAI Grok '
                    'account. All enabled users consume the same Grok '
                    'subscription allowance, and activity is attributable to '
                    'that account. Any subscription or usage costs remain with '
                    'it. xAI accounts are intended for one person—only enable '
                    'this for people or devices you control.'
                : providerUnknown
                    ? 'Cantinarr could not confirm which shared provider is '
                        'selected. If it is an OAuth provider (OpenAI or xAI '
                        'Grok), prompts and tool context will use one shared '
                        'account and its subscription allowance, activity is '
                        'attributable to that account, and any subscription or '
                        'usage costs remain with it. Those accounts are '
                        'intended for one person—only enable this for people '
                        'or devices you control. If it uses an API key, '
                        'requests count against that provider\'s paid quota '
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

  /// Records that this user is an existing account on [server]. The picker
  /// lists what the server reports, administrators marked as such; linking
  /// only records the connection and changes nothing on the server.
  Future<void> _linkMediaAccount(
      UserSummary user, ServiceInstance server) async {
    final remote = await showMediaServerLinkSheet(
      context,
      instanceId: server.id,
      instanceName: server.name,
      serviceType: server.serviceType,
      username: user.username,
    );
    if (remote == null || !mounted) return;
    try {
      await ref.read(mediaAccessServiceProvider).link(
            userId: user.id,
            instanceId: server.id,
            remoteUserId: remote.id,
          );
      await _loadUsers();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(
            'Linked ${remote.name} on ${server.name} to ${user.username}'),
      ));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(_mediaAccessError(e, "Couldn't link the account")),
      ));
    }
  }

  /// Access to a media server IS the instance grant, so this edits the
  /// user's grants for that service type: off removes this instance (the
  /// server then switches the account off, keeping it), on adds it back
  /// (the account comes back). There is no second switch anywhere.
  Future<void> _setMediaAccess(
    UserSummary user,
    ServiceInstance server, {
    required bool enabled,
  }) async {
    try {
      final service =
          RequestSettingsService(backendDio: ref.read(backendClientProvider));
      final grants = await service.getUserInstanceGrants(user.id);
      final current = grants[server.serviceType] ?? const <String>[];
      final next = enabled
          ? {...current, server.id}.toList()
          : current.where((id) => id != server.id).toList();
      await service
          .updateUserInstanceGrants(user.id, {server.serviceType: next});
      await _loadUsers();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text('Turned ${server.name} access ${enabled ? 'on' : 'off'} '
            'for ${user.username}'),
      ));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(
            _friendlyError(e, "Couldn't change ${server.name} access")),
      ));
    }
  }

  /// Forgets the link only. The account on the server and the user's grant
  /// both stay as they are, so this asks first and says exactly that.
  Future<void> _unlinkMediaAccount(
    UserSummary user,
    ServiceInstance server,
    MediaServerAccountRow account,
  ) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Unlink account?'),
        content: Text(
          'Cantinarr will forget that ${user.username} is '
          '${account.remoteUsername} on ${server.name}. The account on '
          '${server.name} stays as it is, and Cantinarr stops managing it '
          'until you link it again.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Unlink'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    try {
      await ref
          .read(mediaAccessServiceProvider)
          .unlink(userId: user.id, instanceId: server.id);
      await _loadUsers();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text('Unlinked ${account.remoteUsername} on ${server.name} '
            'from ${user.username}'),
      ));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(_mediaAccessError(e, "Couldn't unlink the account")),
      ));
    }
  }

  String _mediaAccessError(Object e, String fallback) =>
      e is MediaAccessException && e.message.isNotEmpty ? e.message : fallback;

  String _friendlyError(Object e, String fallback) {
    final msg = e.toString();
    // Surface the backend's error message when present.
    final match = RegExp(r'"error":"([^"]+)"').firstMatch(msg);
    return match != null ? match.group(1)! : fallback;
  }

  /// Opens the import for one media server (a chooser first when there are
  /// several): picked accounts become granted, linked Cantinarr users.
  Future<void> _importFromMediaServer() async {
    final servers = _mediaServers;
    if (servers.isEmpty) return;
    ServiceInstance? server = servers.length == 1 ? servers.single : null;
    server ??= await showAppSheet<ServiceInstance>(
      context,
      builder: (sheetContext) => AppSheet(
        padding: const EdgeInsets.fromLTRB(
          AppTheme.spaceXl,
          0,
          AppTheme.spaceXl,
          AppTheme.spaceXl,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              'Import from which server?',
              style: TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 20,
                fontWeight: FontWeight.bold,
              ),
            ),
            const SizedBox(height: AppTheme.spaceMd),
            for (final candidate in servers)
              ListTile(
                contentPadding: EdgeInsets.zero,
                leading: const Icon(Icons.live_tv_outlined,
                    color: AppTheme.textSecondary),
                title: Text(candidate.name,
                    style: const TextStyle(color: AppTheme.textPrimary)),
                onTap: () => Navigator.of(sheetContext).pop(candidate),
              ),
          ],
        ),
      ),
    );
    if (server == null || !mounted) return;
    final users = _users ?? const <UserSummary>[];
    final imported = await showMediaServerImportSheet(
      context,
      server: server,
      existingUsers: {for (final u in users) u.username: u.id},
      linkedTo: {
        for (final row in _mediaAccounts)
          if (row.instanceId == server.id)
            row.remoteUserId: users
                    .where((u) => u.id == row.userId)
                    .map((u) => u.username)
                    .firstOrNull ??
                'another user',
      },
    );
    if (imported == null || !mounted) return;
    await _loadUsers();
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(imported == 1
          ? 'Imported 1 user from ${server.name}'
          : 'Imported $imported users from ${server.name}'),
    ));
  }

  @override
  Widget build(BuildContext context) {
    final mediaServers = ref.watch(authProvider).valueOrNull?.connection
            ?.mediaServerInstances ??
        const <ServiceInstance>[];
    return Scaffold(
      appBar: AppBar(
        title: const Text('Users'),
        actions: [
          if (mediaServers.isNotEmpty)
            IconButton(
              tooltip: 'Import from a media server',
              icon: const Icon(Icons.group_add_outlined),
              onPressed: _isLoading ? null : _importFromMediaServer,
            ),
          IconButton(
            tooltip: 'Invite a new user',
            icon: const Icon(Icons.person_add_outlined),
            onPressed: _isLoading ? null : _inviteNewUser,
          ),
        ],
      ),
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
    final mediaServers =
        ref.watch(authProvider).valueOrNull?.connection?.mediaServerInstances ??
            const <ServiceInstance>[];
    // The notice takes the first row when the account read failed, so the
    // list still renders every user and the gap is named, not implied.
    final noticeRows = _mediaAccountsFailed ? 1 : 0;

    return RefreshIndicator(
      onRefresh: _loadUsers,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: users.length + noticeRows,
        separatorBuilder: (_, __) =>
            const Divider(height: 1, color: AppTheme.border),
        itemBuilder: (context, index) {
          if (index < noticeRows) {
            return const ListTile(
              leading: Icon(Icons.sync_problem_outlined,
                  color: AppTheme.warning),
              title: Text(
                "Couldn't load media server accounts. Pull to refresh.",
                style: TextStyle(color: AppTheme.warning, fontSize: 13),
              ),
            );
          }
          final user = users[index - noticeRows];
          return _UserTile(
            user: user,
            isSelf: user.id == currentUserId,
            mediaServers: mediaServers,
            mediaAccounts: {
              for (final row in _mediaAccounts)
                if (row.userId == user.id) row.instanceId: row,
            },
            onLinkMediaAccount: (server) => _linkMediaAccount(user, server),
            onSetMediaAccess: (server, enabled) =>
                _setMediaAccess(user, server, enabled: enabled),
            onUnlinkMediaAccount: (server, account) =>
                _unlinkMediaAccount(user, server, account),
            onChangeRole: (role) => _changeRole(user, role),
            onDelete: () => _deleteUser(user),
            onResendInvite: () => _resendInvite(user),
            onSendTestPush: () => _sendTestPush(user),
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
            sharedAiConfigured: _sharedAiConfigured,
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
    required this.mediaServers,
    required this.mediaAccounts,
    required this.onLinkMediaAccount,
    required this.onSetMediaAccess,
    required this.onUnlinkMediaAccount,
    required this.onChangeRole,
    required this.onDelete,
    required this.onResendInvite,
    required this.onSendTestPush,
    required this.onSetAuthMethods,
    required this.onRequestSettings,
    required this.onSetSharedAiAccess,
    required this.sharedAiProvider,
    required this.sharedAiConfigured,
  });

  final UserSummary user;
  final bool isSelf;
  /// Every media server the admin's config lists, and this user's linked
  /// account on each (by instance id; absent = no account linked).
  final List<ServiceInstance> mediaServers;
  final Map<String, MediaServerAccountRow> mediaAccounts;
  final void Function(ServiceInstance server) onLinkMediaAccount;
  final void Function(ServiceInstance server, bool enabled) onSetMediaAccess;
  final void Function(ServiceInstance server, MediaServerAccountRow account)
      onUnlinkMediaAccount;
  final ValueChanged<String> onChangeRole;
  final VoidCallback onDelete;
  final VoidCallback onResendInvite;
  final VoidCallback onSendTestPush;
  final void Function({bool? passwordEnabled, bool? passkeyEnabled})
      onSetAuthMethods;
  final VoidCallback onRequestSettings;
  final ValueChanged<bool> onSetSharedAiAccess;
  final String sharedAiProvider;
  final bool sharedAiConfigured;

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
            // "Invite sent" only for a share Cantinarr itself sent; a share
            // adopted from plex.tv, or the server's owner, is said by the
            // account tag below and nothing was sent. "Asked" is an email
            // with no Plex share yet (the grant toggle below is the tap).
            if (mediaServers.any((server) =>
                server.serviceType == 'plex' &&
                (mediaAccounts[server.id]?.createdByCantinarr ?? false)))
              const _Tag(label: 'Plex invite sent', color: AppTheme.available)
            else if (user.plexEmail.isNotEmpty && user.plexInvitedAt == null)
              const _Tag(
                  label: 'Asked for Plex access', color: AppTheme.requested),
            // One tag per linked media-server account: the server's name
            // alone when the account name matches the Cantinarr username,
            // the remote name otherwise, and ": off" while access is off.
            for (final server in mediaServers)
              if (mediaAccounts[server.id] case final account?)
                account.disabled
                    ? _Tag(
                        label: '${server.name}: off',
                        color: AppTheme.unavailable)
                    : _Tag(
                        label: account.remoteUsername == user.username
                            ? server.name
                            : '${server.name}: ${account.remoteUsername}',
                        color: AppTheme.available),
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
        // Media-server entries carry the instance id after the action.
        final separator = value.indexOf(':');
        if (separator > 0) {
          final action = value.substring(0, separator);
          final instanceId = value.substring(separator + 1);
          for (final server in mediaServers) {
            if (server.id != instanceId) continue;
            final account = mediaAccounts[server.id];
            switch (action) {
              case 'media_link':
                onLinkMediaAccount(server);
              case 'media_access':
                if (account != null) {
                  onSetMediaAccess(server, account.disabled);
                }
              case 'media_unlink':
                if (account != null) onUnlinkMediaAccount(server, account);
            }
            return;
          }
          return;
        }
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
              // An unknown provider (failed fetch) is not the same as a known
              // absence — and the server-defaulted provider name may not
              // claim an allowance that doesn't exist yet.
              !sharedAiConfigured && sharedAiProvider.isNotEmpty
                  ? 'No shared provider configured yet'
                  : sharedAiProvider == 'codex'
                      ? 'Shared OpenAI OAuth allowance'
                      : sharedAiProvider == 'grok_oauth'
                          ? 'Shared xAI Grok allowance'
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
        // Media servers, one set per shared server: link an existing
        // account when none is recorded; otherwise flip access (the grant,
        // which the server mirrors onto the account) or forget the link.
        for (final server in mediaServers)
          if (mediaAccounts[server.id] case final account?) ...[
            PopupMenuItem(
              value: 'media_access:${server.id}',
              child: ListTile(
                leading: Icon(account.disabled
                    ? Icons.play_circle_outline
                    : Icons.block_outlined),
                title: Text(account.disabled
                    ? 'Turn ${server.name} access on'
                    : 'Turn ${server.name} access off'),
                contentPadding: EdgeInsets.zero,
              ),
            ),
            PopupMenuItem(
              value: 'media_unlink:${server.id}',
              child: ListTile(
                leading: const Icon(Icons.link_off),
                title: Text('Unlink ${server.name} account'),
                contentPadding: EdgeInsets.zero,
              ),
            ),
          ] else
            PopupMenuItem(
              value: 'media_link:${server.id}',
              child: ListTile(
                leading: const Icon(Icons.live_tv_outlined),
                title: Text('Link ${server.name} account…'),
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
        // The subtitles frame these as the web sign-in story: connect links
        // sign one device into the app once, while a password or passkey is
        // what survives a cleared browser.
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
              subtitle: user.passwordEnabled
                  ? null
                  : const Text('Lets them sign in on the web'),
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
              subtitle: user.passkeyEnabled
                  ? null
                  : const Text('Lets them sign in on the web (HTTPS)'),
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
