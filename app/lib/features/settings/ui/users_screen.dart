import '../../notifications/ui/notification_labels.dart';
import '../../media_access/ui/media_access_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../ai_assistant/data/ai_settings_service.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/data/server_status.dart';
import '../../auth/logic/auth_provider.dart';
import '../../media_access/data/media_access_service.dart';
import '../../media_access/ui/media_server_link_sheet.dart';
import '../../notifications/push_service.dart';
import '../data/credentials_service.dart';
import '../data/request_settings_service.dart';
import '../logic/plex_invites_provider.dart';
import 'user_onboarding_status.dart';

/// Admin screen for managing user accounts: invite new users, change roles,
/// remove users, and see who still has an outstanding connect-link invite.
class UsersScreen extends ConsumerStatefulWidget {
  const UsersScreen({super.key, this.userId});

  final int? userId;

  @override
  ConsumerState<UsersScreen> createState() => _UsersScreenState();
}

class _UsersScreenState extends ConsumerState<UsersScreen> {
  List<UserSummary>? _users;
  bool _isLoading = true;
  int _statusRevision = 0;
  bool _mediaActionBusy = false;
  String? _error;
  ServerStatus? _serverStatus;
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

  bool get _supportsManagement =>
      ref.read(authProvider).valueOrNull?.connection?.mediaAccountManagement ??
      false;

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
        final response =
            await ref.read(backendClientProvider).get('/api/auth/status');
        _serverStatus = ServerStatus.fromJson(
            Map<String, dynamic>.from(response.data as Map));
      } catch (_) {
        _serverStatus = null;
      }
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
      if (!mounted) return;
      ref.read(plexInvitesWaitingProvider.notifier).refresh();
      setState(() {
        _statusRevision++;
        _users = users;
        _mediaAccounts = mediaAccounts;
        _mediaAccountsFailed = mediaAccountsFailed;
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = context.l10n.settingsFailedToLoadUsers;
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
                  ? context.l10n.settingsMessageValue1IsNowAnAdmin(
                      (user.username).toString())
                  : context.l10n.settingsMessageValue1IsNowAUser(
                      (user.username).toString()),
            ),
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(
                  _friendlyError(e, context.l10n.settingsFailedToChangeRole))),
        );
      }
    }
  }

  Future<void> _deleteUser(UserSummary user) async {
    final managed = _mediaAccounts
        .where((a) => a.userId == user.id && a.manageAccess && !a.administrator)
        .map((a) => a.instanceName)
        .join(', ');
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(context.l10n.settingsRemoveUser),
        content: Text(
          context.l10n
                  .settingsMessageRemoveValue1ThisDeletesTheirAccountDevices(
                      (user.username).toString()) +
              context.l10n.settingsAndAnyPendingInvitesThisCannotBeUndone +
              '${managed.isEmpty ? '' : context.l10n.settingsMessageCantinarrWillTurnOffTheirManagedServerAccess((managed).toString())}'
                  '${_supportsManagement ? context.l10n.settingsNNlinkedOnlyAccountsStayAsTheyAre : ''}'
                  '${_mediaAccountsFailed ? context.l10n.settingsNNmediaAccountDetailsCouldNotBeLoaded : ''}',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: Text(context.l10n.settingsCancel),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(backgroundColor: AppTheme.error),
            onPressed: () => Navigator.pop(context, true),
            child: Text(context.l10n.settingsRemove),
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
          SnackBar(
              content: Text(context.l10n
                  .settingsMessageRemovedValue1((user.username).toString()))),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(
                  _friendlyError(e, context.l10n.settingsFailedToRemoveUser))),
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
      context.l10n.settingsShareThisOneTimeCantinarrLinkWithThem,
    );
  }

  /// Invite someone new: ask for a name, then mint their first connect link.
  /// The connect-token endpoint creates the account on the spot, so the new
  /// row is in the list by the time the link dialog shows.
  Future<void> _inviteNewUser() async {
    final nameController = TextEditingController();
    final name = await showDialog<String>(
      context: context,
      builder: (dialogContext) => UnsavedChangesGuard(
        isDialog: true,
        hasChanges: () => nameController.text.isNotEmpty,
        child: AlertDialog(
          title: Text(context.l10n.settingsInviteANewUser),
          content: TextField(
            controller: nameController,
            autofocus: true,
            decoration: InputDecoration(
              labelText: context.l10n.settingsName,
              hintText: context.l10n.settingsEGMomDadRoommate2,
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
              onPressed: () => Navigator.of(dialogContext).maybePop(),
              child: Text(context.l10n.settingsCancel),
            ),
            ElevatedButton(
              onPressed: () {
                final trimmed = nameController.text.trim();
                if (trimmed.isEmpty) return;
                Navigator.of(dialogContext).pop(trimmed);
              },
              child: Text(context.l10n.settingsCreateInvite),
            ),
          ],
        ),
      ),
    );
    if (name == null || name.isEmpty || !mounted) return;
    await _generateAndShowInviteLink(
      name,
      context.l10n.settingsShareThisOneTimeCantinarrLinkWithThem2,
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
          SnackBar(
              content: Text(
                  _friendlyError(e, context.l10n.settingsFailedToCreateLink))),
        );
      }
      return;
    }

    await _loadUsers();
    if (!mounted) return;

    final newLink = link;
    final invitedUser =
        _users?.where((user) => user.username == username).firstOrNull;
    final ssoRequired =
        _serverStatus?.ssoOnly == true && invitedUser?.isAdmin != true;
    await showDialog<void>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(context.l10n
            .settingsMessageInviteLinkForValue1((username).toString())),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              '$description '
              '${ssoRequired ? context.l10n.settingsMessageTheyContinueThroughValue1ToSignInThe((_serverStatus!.ssoProvider).toString()) : _serverStatus == null ? context.l10n.settingsSignInRequirementsCouldNotBeVerifiedYour : context.l10n.settingsItConnectsOneDeviceToTheirCantinarrAccount}',
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
              Text(
                context.l10n.settingsThisLinkUsesTheAddressYourAppConnects,
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 12),
              ),
            ],
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(),
            child: Text(context.l10n.settingsDone),
          ),
          ElevatedButton.icon(
            onPressed: () {
              Clipboard.setData(ClipboardData(text: newLink));
              ScaffoldMessenger.of(dialogContext).showSnackBar(
                SnackBar(content: Text(context.l10n.settingsLinkCopied)),
              );
            },
            icon: const Icon(Icons.copy, size: 18),
            label: Text(context.l10n.settingsCopy),
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
          content: Text(localizedDescribePushTest(context, result,
              username: user.username)),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
            content: Text(
                _friendlyError(e, context.l10n.settingsFailedToSendTestPush))),
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
          title: Text(isPassword
              ? context.l10n.settingsDisablePassword
              : context.l10n.settingsDisablePasskeys),
          content: Text(
            isPassword
                ? context.l10n
                        .settingsMessageThisClearsValue1SPasswordTheyCanSet(
                            (user.username).toString()) +
                    context.l10n.settingsOneOnlyIfYouReEnableIt
                : context.l10n
                        .settingsMessageThisDeletesValue1SPasskeysTheyLlNeed(
                            (user.username).toString()) +
                    context.l10n.settingsRegisterAgainIfYouReEnableThem,
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: Text(context.l10n.settingsCancel),
            ),
            ElevatedButton(
              style: ElevatedButton.styleFrom(backgroundColor: AppTheme.error),
              onPressed: () => Navigator.pop(context, true),
              child: Text(context.l10n.settingsDisable),
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
          SnackBar(
              content: Text(context.l10n
                  .settingsMessageUpdatedValue1SSignInMethods(
                      (user.username).toString()))),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(_friendlyError(
                e, context.l10n.settingsFailedToUpdateSignInMethods)),
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
          title: Text(context.l10n.settingsMessageIncludeAiAccessForValue1(
              (user.username).toString())),
          content: Text(
            unconfigured
                ? context.l10n.settingsNoSharedAiProviderIsSetUpYet
                : codex
                    ? context.l10n.settingsPromptsAndToolContextWillUseTheShared
                    : grokOAuth
                        ? context
                            .l10n.settingsPromptsAndToolContextWillUseTheShared2
                        : providerUnknown
                            ? context.l10n
                                .settingsCantinarrCouldNotConfirmWhichSharedProviderIs
                            : context.l10n
                                .settingsThisUserCanSendPromptsAndToolContext,
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(false),
              child: Text(context.l10n.settingsCancel),
            ),
            ElevatedButton(
              onPressed: () => Navigator.of(dialogContext).pop(true),
              child: Text(context.l10n.settingsIncludeAiAccess),
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
              ? context.l10n.settingsMessageIncludedAiEnabledForValue1(
                  (user.username).toString())
              : context.l10n.settingsMessageIncludedAiRemovedForValue1(
                  (user.username).toString())),
        ),
      );
    } catch (error) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(_friendlyError(
              error, context.l10n.settingsFailedToUpdateAiAccess)),
        ),
      );
    }
  }

  /// Records that this user is an existing account on [server]. The picker
  /// lists what the server reports, administrators marked as such; linking
  /// only records the connection and changes nothing on the server.
  Future<void> _linkMediaAccount(
      UserSummary user, ServiceInstance server) async {
    final choice = await showMediaServerLinkSheet(
      context,
      instanceId: server.id,
      instanceName: server.name,
      serviceType: server.serviceType,
      username: user.username,
    );
    if (choice == null || !mounted) return;
    final remote = choice.account;
    try {
      final linked = await ref.read(mediaAccessServiceProvider).link(
            userId: user.id,
            instanceId: server.id,
            remoteUserId: remote.id,
            manageAccess: choice.manageAccess,
          );
      await _loadUsers();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(context.l10n
            .settingsMessageLinkedValue1OnValue2ToValue3Value4(
                (remote.name).toString(),
                (server.name).toString(),
                (user.username).toString(),
                (linked.plexIdentityError.isEmpty
                        ? ''
                        : context.l10n
                            .settingsMessagePlexSignInWasNotLinkedValue1(
                                (linked.plexIdentityError).toString()))
                    .toString())),
      ));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(
            _mediaAccessError(e, context.l10n.settingsCouldnTLinkTheAccount)),
      ));
    }
  }

  /// Changes the Cantinarr grant. Only managed links propagate it remotely.
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
      final pending = _mediaAccounts.any((account) =>
          account.userId == user.id &&
          account.instanceId == server.id &&
          account.accessSyncPending);
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(context.l10n
            .settingsMessageTurnedValue1AccessInCantinarrValue2ForValue3(
                (server.name).toString(),
                (enabled ? context.l10n.settingsOn2 : context.l10n.settingsOff2)
                    .toString(),
                (user.username).toString(),
                (pending
                        ? context.l10n
                            .settingsServerAccessChangePendingCantinarrWillRetry
                        : '')
                    .toString())),
      ));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(_friendlyError(
            e,
            context.l10n.settingsMessageCouldnTChangeValue1Access(
                (server.name).toString()))),
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
        title: Text(context.l10n.settingsUnlinkAccount),
        content: Text(
          context.l10n.settingsMessageCantinarrWillForgetThatValue1IsValue2On(
                  (user.username).toString(),
                  (account.remoteUsername).toString(),
                  (server.name).toString(),
                  (server.name).toString()) +
              context.l10n.settingsLinkItExplicitlyToConnectItAgain,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: Text(context.l10n.settingsCancel),
          ),
          ElevatedButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(context.l10n.settingsUnlink),
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
        content: Text(context.l10n
            .settingsMessageUnlinkedValue1OnValue2FromValue3(
                (account.remoteUsername).toString(),
                (server.name).toString(),
                (user.username).toString())),
      ));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(
            _mediaAccessError(e, context.l10n.settingsCouldnTUnlinkTheAccount)),
      ));
    }
  }

  Future<void> _setMediaManagement(UserSummary user, ServiceInstance server,
      MediaServerAccountRow account) async {
    final manage = !account.manageAccess;
    final remoteAction = server.serviceType == 'plex'
        ? (account.granted
            ? context.l10n.settingsRestoreTheirPlexShareIfNeeded
            : context.l10n.settingsRemoveTheirPlexShare)
        : (account.granted
            ? context.l10n.settingsEnableThisAccount
            : context.l10n.settingsDisableThisAccount);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(manage
            ? context.l10n
                .settingsMessageManageAccessOnValue1((server.name).toString())
            : context.l10n.settingsStopManagingAccess),
        content: Text(manage
            ? context.l10n.settingsMessageCantinarrWillValue1ToMatchThisUserS(
                (remoteAction).toString(),
                (server.serviceType == 'plex'
                        ? context.l10n
                            .settingsRestoringARemovedPlexShareUsesThisServer
                        : '')
                    .toString())
            : context.l10n.settingsMessageTheLinkAndAccountOnValue1StayAs(
                (server.name).toString())),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: Text(context.l10n.settingsCancel)),
          FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: Text(manage
                  ? context.l10n.settingsManageAccess
                  : context.l10n.settingsStopManaging)),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      final result = await ref.read(mediaAccessServiceProvider).setManagement(
          userId: user.id, instanceId: server.id, manageAccess: manage);
      await _loadUsers();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text(
        result.accessSyncPending
            ? context.l10n.settingsManagementSavedTheServerAccessChangeIsPending
            : manage
                ? context.l10n.settingsMessageCantinarrNowManagesAccessOnValue1(
                    (server.name).toString())
                : context.l10n.settingsMessageStoppedManagingAccessOnValue1(
                    (server.name).toString()),
      )));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text(_mediaAccessError(
              e, context.l10n.settingsCouldnTChangeAccountManagement))));
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

  Future<void> _importFromMediaServer() async {
    await context.push('/settings/users/import');
    if (mounted) await _loadUsers();
  }

  Future<void> _openUser(UserSummary user) async {
    await context.push('/settings/users/${user.id}/access');
    if (mounted) await _loadUsers();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.userId == null
            ? context.l10n.settingsUsers
            : context.l10n.settingsUserAccess),
      ),
      body: CenteredContent(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (widget.userId == null)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 16),
                child: Wrap(
                  spacing: 12,
                  runSpacing: 8,
                  children: [
                    ElevatedButton.icon(
                      onPressed: _importFromMediaServer,
                      icon: const Icon(Icons.group_add_outlined),
                      label: Text(context.l10n.settingsImportUsers),
                    ),
                    OutlinedButton.icon(
                      onPressed: _isLoading ? null : _inviteNewUser,
                      icon: const Icon(Icons.person_add_outlined),
                      label: Text(context.l10n.settingsInviteUser),
                    ),
                  ],
                ),
              ),
            Expanded(child: _buildBody()),
          ],
        ),
      ),
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
            ElevatedButton(
                onPressed: _loadUsers, child: Text(context.l10n.settingsRetry)),
          ],
        ),
      );
    }

    final users = _users ?? [];
    if (widget.userId != null) {
      final user = users.where((user) => user.id == widget.userId).firstOrNull;
      if (user == null) {
        return Center(
            child: Text(context.l10n.settingsThisUserIsNoLongerAvailable));
      }
      return RefreshIndicator(
        onRefresh: _loadUsers,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            _buildUserTile(user, showMediaActions: false),
            const SizedBox(height: 16),
            UserOnboardingStatus(
              key: ValueKey(_statusRevision),
              userId: user.id,
              onIssueInvitation: () => _resendInvite(user),
              mediaAccountActions: (server, account) =>
                  _mediaAccountActions(user, server, account),
            ),
          ],
        ),
      );
    }
    if (users.isEmpty) {
      return Center(
        child: Text(
          context.l10n.settingsNoUsersYet,
          style: TextStyle(color: AppTheme.textSecondary),
        ),
      );
    }

    final mediaServers =
        ref.watch(authProvider).valueOrNull?.connection?.mediaServerInstances ??
            const <ServiceInstance>[];
    // The notice takes the first row when the account read failed, so the
    // list still renders every user and the gap is named, not implied.
    final showPlexHelp =
        mediaServers.any((server) => server.serviceType == 'plex');
    final failureRows = _mediaAccountsFailed ? 1 : 0;
    final noticeRows = failureRows + (showPlexHelp ? 1 : 0);

    return RefreshIndicator(
      onRefresh: _loadUsers,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: users.length + noticeRows,
        separatorBuilder: (_, __) =>
            const Divider(height: 1, color: AppTheme.border),
        itemBuilder: (context, index) {
          if (index < failureRows) {
            return ListTile(
              leading:
                  Icon(Icons.sync_problem_outlined, color: AppTheme.warning),
              title: Text(
                context.l10n.settingsCouldnTLoadMediaServerAccountsPullTo,
                style: TextStyle(color: AppTheme.warning, fontSize: 13),
              ),
            );
          }
          if (index < noticeRows) {
            return ListTile(
              leading: Icon(Icons.info_outline, color: AppTheme.textSecondary),
              title: Text(
                  context.l10n.settingsPlexLibraryAccessAndSignInAreSeparate),
              subtitle: Text(
                context.l10n
                    .settingsSendingALibraryInvitationRecordsTheMediaAccount,
              ),
            );
          }
          final user = users[index - noticeRows];
          return _buildUserTile(user, onOpen: () => _openUser(user));
        },
      ),
    );
  }

  Future<void> _runMediaAction(Future<void> Function() action) async {
    if (_mediaActionBusy) return;
    setState(() => _mediaActionBusy = true);
    try {
      await action();
    } finally {
      if (mounted) setState(() => _mediaActionBusy = false);
    }
  }

  Widget _mediaAccountActions(UserSummary user, ServiceInstance server,
      MediaServerAccountRow? account) {
    if (account == null) {
      return OutlinedButton.icon(
        onPressed: _mediaActionBusy
            ? null
            : () => _runMediaAction(() => _linkMediaAccount(user, server)),
        icon: const Icon(Icons.link),
        label: Text(context.l10n.settingsMessageLinkValue1Account(
            (mediaServerTypeLabel(server.serviceType)).toString())),
      );
    }
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(
        account.manageAccess && !account.administrator
            ? context.l10n
                .settingsMessageChangingThisCantinarrGrantAlsoChangesAccessOn(
                    (server.name).toString())
            : context.l10n
                .settingsChangingThisCantinarrGrantLeavesServerAccessUnchanged,
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
      ),
      Wrap(spacing: 8, runSpacing: 8, children: [
        TextButton.icon(
          onPressed: _mediaActionBusy
              ? null
              : () => _runMediaAction(() =>
                  _setMediaAccess(user, server, enabled: !account.granted)),
          icon: Icon(account.granted
              ? Icons.block_outlined
              : Icons.play_circle_outline),
          label: Text(context.l10n.settingsMessageTurnCantinarrAccessValue1(
              (account.granted
                      ? context.l10n.settingsOff2
                      : context.l10n.settingsOn2)
                  .toString())),
        ),
        if (_supportsManagement && !account.administrator)
          TextButton.icon(
            onPressed: _mediaActionBusy
                ? null
                : () => _runMediaAction(
                    () => _setMediaManagement(user, server, account)),
            icon: const Icon(Icons.manage_accounts_outlined),
            label: Text(account.manageAccess
                ? context.l10n.settingsStopManagingServerAccess
                : context.l10n.settingsManageServerAccess),
          ),
        TextButton.icon(
          onPressed: _mediaActionBusy
              ? null
              : () => _runMediaAction(
                  () => _unlinkMediaAccount(user, server, account)),
          icon: const Icon(Icons.link_off),
          label: Text(context.l10n.settingsUnlinkAccount2),
        ),
      ]),
    ]);
  }

  Widget _buildUserTile(UserSummary user,
      {VoidCallback? onOpen, bool showMediaActions = true}) {
    final currentUserId = ref.read(authProvider).valueOrNull?.user?.id;
    final mediaServers = _mediaServers;
    return _UserTile(
      user: user,
      onOpen: onOpen,
      showMediaActions: showMediaActions,
      isSelf: user.id == currentUserId,
      mediaServers: mediaServers,
      supportsManagement: _supportsManagement,
      onSetManagement: (server, account) =>
          _setMediaManagement(user, server, account),
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
      // The per-user screen leads with the kids account switch, so
      // the row's Child tag is re-read once it pops.
      onRequestSettings: () => context
          .push(
        '/settings/users/${user.id}/request-settings'
        '${user.isAdmin ? '?admin=1' : ''}',
        extra: user.username,
      )
          .then((_) {
        if (mounted) _loadUsers();
      }),
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
  }
}

class _UserTile extends StatelessWidget {
  const _UserTile({
    required this.user,
    this.onOpen,
    this.showMediaActions = true,
    required this.isSelf,
    required this.mediaServers,
    required this.supportsManagement,
    required this.onSetManagement,
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
  final VoidCallback? onOpen;
  final bool showMediaActions;
  final bool isSelf;

  /// Every media server the admin's config lists, and this user's linked
  /// account on each (by instance id; absent = no account linked).
  final List<ServiceInstance> mediaServers;
  final bool supportsManagement;
  final void Function(ServiceInstance server, MediaServerAccountRow account)
      onSetManagement;
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
      onTap: onOpen,
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
            _Tag(label: context.l10n.settingsYou, color: AppTheme.accent),
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
              label: user.isAdmin
                  ? context.l10n.settingsAdmin
                  : context.l10n.settingsUser,
              color: user.isAdmin ? AppTheme.accent : AppTheme.textSecondary,
            ),
            if (user.ssoLinked)
              _Tag(
                  label: context.l10n.settingsSsoLinked,
                  color: AppTheme.available),
            if (user.child)
              _Tag(label: context.l10n.settingsChild, color: AppTheme.info),
            if (user.hasPendingInvite)
              _Tag(
                  label: context.l10n.settingsInvited,
                  color: AppTheme.requested)
            else if (_needsInvite)
              _Tag(
                  label: context.l10n.settingsInviteExpired,
                  color: AppTheme.unavailable),
            _Tag(
              label: user.deviceCount == 1
                  ? context.l10n.settings1Device
                  : context.l10n.settingsMessageValue1Devices(
                      (user.deviceCount).toString()),
              color: user.deviceCount > 0
                  ? AppTheme.available
                  : AppTheme.unavailable,
            ),
            if (user.passwordEnabled)
              _Tag(
                  label: context.l10n.settingsPassword,
                  color: AppTheme.textSecondary),
            if (user.passkeyEnabled)
              _Tag(
                  label: context.l10n.settingsPasskey,
                  color: AppTheme.textSecondary),
            if (user.sharedAiEnabled)
              _Tag(
                  label: context.l10n.settingsAiIncluded,
                  color: AppTheme.signal),
            if (user.plexEmail.isNotEmpty)
              _Tag(label: user.plexEmail, color: AppTheme.textSecondary),
            if (user.plexEmail.isNotEmpty &&
                user.plexInvitedAt == null &&
                !mediaServers.any((server) =>
                    server.serviceType == 'plex' &&
                    mediaAccounts.containsKey(server.id)))
              _Tag(
                  label: context.l10n.settingsAskedForPlexAccess,
                  color: AppTheme.requested),
            // One tag per linked media-server account: the server's name
            // alone when the account name matches the Cantinarr username,
            // the remote name otherwise, and ": off" while access is off.
            for (final server in mediaServers)
              if (mediaAccounts[server.id] case final account?)
                _Tag(
                  label: supportsManagement
                      ? '${server.name}${account.remoteUsername == user.username ? '' : ' (${account.remoteUsername})'}: ${account.granted ? context.l10n.notificationGranted : context.l10n.settingsNoGrant} · ${mediaAccessLabel(context, account.managementLabel)} · ${mediaAccessLabel(context, account.accessLabel)}'
                      : account.disabled
                          ? context.l10n.settingsMessageValue1Off(
                              (server.name).toString())
                          : account.remoteUsername == user.username
                              ? server.name
                              : '${server.name}: ${account.remoteUsername}',
                  color: account.accessSyncPending ||
                          !account.verified ||
                          account.pending == true ||
                          (server.serviceType == 'plex' &&
                              account.pending == null)
                      ? AppTheme.warning
                      : account.granted
                          ? AppTheme.available
                          : AppTheme.unavailable,
                ),
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
                  onSetMediaAccess(server, !account.granted);
                }
              case 'media_manage':
                if (account != null) onSetManagement(server, account);
              case 'media_unlink':
                if (account != null) onUnlinkMediaAccount(server, account);
            }
            return;
          }
          return;
        }
        switch (value) {
          case 'sso':
            context.push('/settings/users/${user.id}/sso');
            break;
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
        PopupMenuItem(
            value: 'sso', child: Text(context.l10n.settingsLinkedSignIn)),
        PopupMenuItem(
          value: 'request_settings',
          child: ListTile(
            leading: Icon(Icons.tune),
            title: Text(context.l10n.settingsUserSettings),
            contentPadding: EdgeInsets.zero,
          ),
        ),
        PopupMenuItem(
          value: 'toggle_shared_ai',
          child: ListTile(
            leading: const Icon(Icons.auto_awesome_outlined),
            title: Text(context.l10n.settingsIncludedAiAccess),
            subtitle: Text(
              // An unknown provider (failed fetch) is not the same as a known
              // absence — and the server-defaulted provider name may not
              // claim an allowance that doesn't exist yet.
              !sharedAiConfigured && sharedAiProvider.isNotEmpty
                  ? context.l10n.settingsNoSharedProviderConfiguredYet
                  : sharedAiProvider == 'codex'
                      ? context.l10n.settingsSharedOpenaiOauthAllowance
                      : sharedAiProvider == 'grok_oauth'
                          ? context.l10n.settingsSharedXaiGrokAllowance
                          : sharedAiProvider.isEmpty
                              ? context.l10n.settingsProviderStatusUnavailable
                              : context.l10n.settingsServerProviderQuota,
            ),
            trailing: IgnorePointer(
              child: Switch(
                value: user.sharedAiEnabled,
                onChanged: (_) {},
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
                    ? context.l10n.settingsNewInviteLink
                    : (_needsInvite
                        ? context.l10n.settingsReInvite
                        : context.l10n.settingsIssueDeviceLink),
              ),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        // Media servers, one set per shared server: link an existing
        // account when none is recorded; otherwise flip access (the grant,
        // which the server mirrors onto the account) or forget the link.
        for (final server
            in showMediaActions ? mediaServers : const <ServiceInstance>[])
          if (mediaAccounts[server.id] case final account?) ...[
            PopupMenuItem(
              value: 'media_access:${server.id}',
              child: ListTile(
                leading: Icon(!account.granted
                    ? Icons.play_circle_outline
                    : Icons.block_outlined),
                title: Text(!account.granted
                    ? context.l10n.settingsMessageTurnValue1AccessOn(
                        (server.name).toString())
                    : context.l10n.settingsMessageTurnValue1AccessOff(
                        (server.name).toString())),
                subtitle: supportsManagement
                    ? Text(account.manageAccess && !account.administrator
                        ? context.l10n.settingsAlsoChangesServerAccess
                        : context
                            .l10n.settingsCantinarrOnlyServerAccessStaysTheSame)
                    : null,
                contentPadding: EdgeInsets.zero,
              ),
            ),
            if (supportsManagement && !account.administrator)
              PopupMenuItem(
                value: 'media_manage:${server.id}',
                child: ListTile(
                  leading: const Icon(Icons.manage_accounts_outlined),
                  title: Text(account.manageAccess
                      ? context.l10n.settingsMessageStopManagingValue1Access(
                          (server.name).toString())
                      : context.l10n.settingsMessageManageValue1Access(
                          (server.name).toString())),
                  contentPadding: EdgeInsets.zero,
                ),
              ),
            PopupMenuItem(
              value: 'media_unlink:${server.id}',
              child: ListTile(
                leading: const Icon(Icons.link_off),
                title: Text(context.l10n.settingsMessageUnlinkValue1Account(
                    (server.name).toString())),
                contentPadding: EdgeInsets.zero,
              ),
            ),
          ] else
            PopupMenuItem(
              value: 'media_link:${server.id}',
              child: ListTile(
                leading: const Icon(Icons.live_tv_outlined),
                title: Text(context.l10n.settingsMessageLinkValue1Account2(
                    (server.name).toString())),
                contentPadding: EdgeInsets.zero,
              ),
            ),
        if (!isSelf)
          PopupMenuItem(
            value: 'test_push',
            child: ListTile(
              leading: Icon(Icons.notifications_active_outlined),
              title: Text(context.l10n.settingsSendTestPush),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (!user.isAdmin)
          PopupMenuItem(
            value: 'make_admin',
            child: ListTile(
              leading: Icon(Icons.arrow_upward),
              title: Text(context.l10n.settingsMakeAdmin),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (user.isAdmin && !isSelf)
          PopupMenuItem(
            value: 'make_user',
            child: ListTile(
              leading: Icon(Icons.arrow_downward),
              title: Text(context.l10n.settingsMakeUser),
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
                  ? context.l10n.settingsDisablePassword2
                  : context.l10n.settingsEnablePassword),
              subtitle: user.passwordEnabled
                  ? null
                  : Text(context.l10n.settingsLetsThemSignInOnTheWeb),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (!user.isAdmin)
          PopupMenuItem(
            value: user.passkeyEnabled ? 'disable_passkey' : 'enable_passkey',
            child: ListTile(
              leading: const Icon(Icons.fingerprint),
              title: Text(user.passkeyEnabled
                  ? context.l10n.settingsDisablePasskeys2
                  : context.l10n.settingsEnablePasskeys),
              subtitle: user.passkeyEnabled
                  ? null
                  : Text(context.l10n.settingsLetsThemSignInOnTheWebHttps),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (!isSelf)
          PopupMenuItem(
            value: 'delete',
            child: ListTile(
              leading: Icon(Icons.delete_outline, color: AppTheme.error),
              title: Text(context.l10n.settingsRemove,
                  style: TextStyle(color: AppTheme.error)),
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
