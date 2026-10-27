import '../../media_availability/ui/library_access_summary.dart';
import '../../media_access/ui/media_access_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/models/backend_connection.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/data/server_status.dart';
import '../../auth/logic/auth_provider.dart';
import '../../media_access/data/media_access_service.dart';
import '../data/instance_api_service.dart';
import '../data/request_settings_service.dart';

/// A refreshable view of the three independent parts of joining Cantinarr.
/// Missing reads remain unknown; an account link never proves login or playback.
class UserOnboardingStatus extends ConsumerStatefulWidget {
  final int userId;
  final String? invitationLink;
  final String? originSource;
  final bool showMediaAccounts;
  final VoidCallback? onIssueInvitation;
  final Widget Function(ServiceInstance server, MediaServerAccountRow? account)?
      mediaAccountActions;

  const UserOnboardingStatus({
    super.key,
    required this.userId,
    this.invitationLink,
    this.originSource,
    this.showMediaAccounts = true,
    this.onIssueInvitation,
    this.mediaAccountActions,
  });

  @override
  ConsumerState<UserOnboardingStatus> createState() =>
      _UserOnboardingStatusState();
}

class _UserOnboardingStatusState extends ConsumerState<UserOnboardingStatus> {
  UserSummary? _user;
  ServerStatus? _status;
  Map<String, dynamic>? _plex;
  bool? _plexLinked;
  Map<String, List<String>>? _grants;
  List<MediaServerAccountRow>? _accounts;
  bool _loading = true;
  int _generation = 0;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(covariant UserOnboardingStatus oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.userId != widget.userId) _load();
  }

  Future<T?> _read<T>(Future<T> Function() read) async {
    try {
      return await read();
    } catch (_) {
      return null;
    }
  }

  Future<void> _load() async {
    final generation = ++_generation;
    setState(() => _loading = true);
    final dio = ref.read(backendClientProvider);
    final userId = widget.userId;
    final hasPlex = ref
            .read(authProvider)
            .valueOrNull
            ?.connection
            ?.mediaServerInstances
            .any((s) => s.serviceType == 'plex') ??
        false;
    final results = await Future.wait<Object?>([
      _read<UserSummary?>(() async {
        final users = await ref.read(authProvider.notifier).listUsers();
        return users.where((u) => u.id == userId).firstOrNull;
      }),
      _read(() async => ServerStatus.fromJson(Map<String, dynamic>.from(
          (await dio.get('/api/auth/status')).data as Map))),
      _read(() => RequestSettingsService(backendDio: dio)
          .getUserInstanceGrants(userId)),
      _read(() => ref.read(mediaAccessServiceProvider).listAccounts()),
      if (hasPlex)
        _read(() async => Map<String, dynamic>.from(
            (await dio.get('/api/admin/plex-auth')).data as Map)),
      if (hasPlex)
        _read(() async {
          final data =
              (await dio.get('/api/admin/users/$userId/plex')).data as Map;
          return (data['identities'] as List).isNotEmpty;
        }),
    ]);
    if (!mounted || generation != _generation) return;
    setState(() {
      _user = results[0] as UserSummary?;
      _status = results[1] as ServerStatus?;
      _grants = results[2] as Map<String, List<String>>?;
      _accounts = (results[3] as List<MediaServerAccountRow>?)
          ?.where((a) => a.userId == userId)
          .toList();
      _plex = hasPlex ? results[4] as Map<String, dynamic>? : null;
      _plexLinked = hasPlex ? results[5] as bool? : null;
      _loading = false;
    });
  }

  Future<void> _open(String route) async {
    await context.push(route, extra: _user?.username);
    if (mounted) await _load();
  }

  Widget _section(String title, List<Widget> children) => Padding(
        padding: const EdgeInsets.only(bottom: 24),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text(title, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          ...children,
        ]),
      );

  Widget _message(String text) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child:
            Text(text, style: const TextStyle(color: AppTheme.textSecondary)),
      );

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const Padding(
        padding: EdgeInsets.all(24),
        child: Center(child: CircularProgressIndicator()),
      );
    }
    final connection = ref.watch(authProvider).valueOrNull?.connection;
    final instances = connection?.instances ?? const <ServiceInstance>[];
    final hasPlex = instances.any((s) => s.serviceType == 'plex');
    final mediaServers =
        connection?.mediaServerInstances ?? const <ServiceInstance>[];
    final ssoRequired = _status?.ssoOnly == true && _user?.isAdmin == false;
    final hasLink = widget.invitationLink?.isNotEmpty == true;
    final requestTypes = {
      'radarr': context.l10n.settingsMovies,
      'sonarr': context.l10n.settingsTv,
      'chaptarr': context.l10n.settingsBooks,
      'lidarr': context.l10n.settingsMusic
    };
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      _section(context.l10n.settingsCantinarrSignIn, [
        if (_status == null || _user == null)
          _message(
              context.l10n.settingsCouldNotVerifyCantinarrSignInSettingsRefresh)
        else if (ssoRequired) ...[
          _message(context.l10n.settingsMessageSingleSignOnRequiredValue1(
              (_status!.ssoProvider).toString())),
          if (!_status!.ssoAvailable)
            _message(context
                .l10n.settingsTheSingleSignOnProviderIsUnavailableReview),
          if (hasLink)
            _message(context.l10n
                .settingsMessageShareTheInvitationTheRecipientContinuesWithValue1(
                    (_status!.ssoProvider).toString())),
        ] else ...[
          if (_user!.hasPassword && _user!.passwordEnabled)
            _message(context.l10n.settingsACantinarrPasswordIsConfigured),
          if (_user!.passkeyEnabled)
            _message(
                context.l10n.settingsPasskeysAreAllowedTheUserMustRegisterA),
          if (_user!.ssoLinked)
            _message(context.l10n.settingsASingleSignOnIdentityIsLinked),
          if (hasLink)
            _message(context
                .l10n.settingsThisInvitationConnectsOneDeviceAndExpiresIn),
          if (!hasLink && _user!.hasPendingInvite)
            _message(context
                .l10n.settingsAnInvitationIsOutstandingItsExistingLinkIs),
          if (!hasLink &&
              !_user!.hasPendingInvite &&
              _user!.deviceCount == 0 &&
              !(_user!.hasPassword && _user!.passwordEnabled) &&
              !_user!.ssoLinked &&
              !(_plexLinked == true && _plex?['enabled'] == true))
            _message(context
                .l10n.settingsNoCurrentInvitationOrConnectedDeviceIssueAn),
        ],
        if (hasPlex) ...[
          if (_plexLinked == null || _plex == null)
            _message(context.l10n.settingsCouldNotVerifyPlexSignInAMedia)
          else ...[
            _message(_plexLinked!
                ? context.l10n.settingsPlexSignInIdentityConfirmed
                : context.l10n.settingsNoConfirmedPlexSignInIdentity),
            _message(_plex!['enabled'] == true
                ? context.l10n.settingsPlexSignInIsEnabled
                : context.l10n.settingsPlexSignInIsOff),
            _message(_plex!['auto_create'] == true
                ? context
                    .l10n.settingsAutomaticSignupIsEnabledForEligiblePlexUsers
                : context.l10n.settingsAutomaticSignupIsOffImportedUsersDoNot),
            if (ssoRequired)
              _message(
                  context.l10n.settingsPlexDoesNotSatisfyTheRequiredSingleSign),
          ],
          TextButton(
            onPressed: () => _open('/settings/plex-auth'),
            child: Text(context.l10n.settingsReviewPlexSignIn),
          ),
        ],
        _message(context
            .l10n.settingsJellyfinEmbyAndAudiobookshelfPasswordsAreUsedOn),
        Wrap(spacing: 8, runSpacing: 8, children: [
          if (!hasLink && _user != null)
            TextButton(
              onPressed: widget.onIssueInvitation ??
                  () => _open('/settings/users/${widget.userId}/access'),
              child: Text(_user!.hasPendingInvite
                  ? context.l10n.settingsReplaceInvitation
                  : context.l10n.settingsIssueInvitation),
            ),
          TextButton(
              onPressed: () => _open('/settings/users/${widget.userId}/sso'),
              child: Text(context.l10n.settingsLinkedSignIn)),
          if (hasLink)
            TextButton.icon(
              onPressed: () async {
                await Clipboard.setData(
                    ClipboardData(text: widget.invitationLink!));
                if (context.mounted) {
                  ScaffoldMessenger.of(context).showSnackBar(SnackBar(
                      content: Text(context.l10n.settingsInvitationCopied)));
                }
              },
              icon: const Icon(Icons.copy, size: 18),
              label: Text(context.l10n.settingsCopyInvitation),
            ),
        ]),
        if (hasLink && widget.originSource == 'app')
          _message(
              context.l10n.settingsThisInvitationUsesTheAddressThisAppConnects),
      ]),
      _section(context.l10n.settingsRequestPermissions, [
        if (_grants == null)
          _message(
              context.l10n.settingsCouldNotLoadRequestAssignmentsRefreshToRetry)
        else ...[
          for (final type in requestTypes.entries)
            _message('${type.value}: ${_assignmentNames(type.key, instances)}'),
          _message(
              context.l10n.settingsNoRequestAssignmentsIsAValidChoiceLibrary),
        ],
        if (_user != null &&
            !_user!.isAdmin &&
            !_user!.permissions.contains('media:request'))
          _message(
              context.l10n.settingsThisAccountDoesNotHavePermissionToRequest),
        Wrap(spacing: 8, runSpacing: 8, children: [
          TextButton(
              onPressed: () async {
                await assignRequestLibraries(context, ref, [widget.userId]);
                if (mounted) await _load();
              },
              child: Text(context.l10n.settingsAssignRequestLibraries)),
          TextButton(
              onPressed: () => _open(
                  '/settings/users/${widget.userId}/request-settings${_user?.isAdmin == true ? '?admin=1' : ''}'),
              child: Text(context.l10n.settingsRequestSettings2)),
        ]),
      ]),
      if (widget.showMediaAccounts)
        _section(context.l10n.settingsMediaServerAccounts, [
          if (_accounts == null)
            _message(
                context.l10n.settingsCouldNotVerifyMediaServerAccountsRefreshTo)
          else ...[
            if (mediaServers.isEmpty && _accounts!.isEmpty)
              _message(context.l10n.settingsNoMediaServersAreConfigured),
            for (final server in mediaServers)
              _mediaAccountRow(
                  server,
                  _accounts!
                      .where((account) => account.instanceId == server.id)
                      .firstOrNull),
            // Keep stored links visible if config no longer lists their server,
            // but never offer a mutation against an unknown instance.
            for (final account in _accounts!.where((account) =>
                !mediaServers.any((server) => server.id == account.instanceId)))
              _mediaAccountRow(
                ServiceInstance(
                    id: account.instanceId,
                    serviceType: account.serviceType,
                    name: account.instanceName),
                account,
                showActions: false,
              ),
          ],
          _message(context
              .l10n.settingsLinkedAccountsAndActiveServerAccessAreSeparate),
          if (connection?.mediaLibraryAvailability == true)
            LibraryAccessSummary(userId: widget.userId),
        ]),
      TextButton.icon(
          onPressed: _load,
          icon: const Icon(Icons.refresh),
          label: Text(context.l10n.settingsRefreshAccessStatus)),
    ]);
  }

  Widget _mediaAccountRow(
          ServiceInstance server, MediaServerAccountRow? account,
          {bool showActions = true}) =>
      Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          ListTile(
            contentPadding: EdgeInsets.zero,
            title: Text(
                '${mediaServerTypeLabel(server.serviceType)} · ${server.name}'),
            subtitle: Text(account == null
                ? context.l10n.settingsNoAccountLinked
                : '${account.remoteUsername}\n${mediaAccessLabel(context, account.managementLabel)}\n${mediaAccessLabel(context, account.accessLabel)}${account.granted ? '' : context.l10n.settingsNnoCantinarrLibraryGrant}'),
            isThreeLine: account != null,
          ),
          if (showActions && widget.mediaAccountActions != null)
            widget.mediaAccountActions!(server, account),
        ]),
      );

  String _assignmentNames(String type, List<ServiceInstance> instances) {
    final ids = _grants![type] ?? const [];
    if (ids.isEmpty) return context.l10n.settingsNoLibrariesAssigned;
    return ids
        .map((id) =>
            instances.where((i) => i.id == id).firstOrNull?.name ??
            context.l10n.settingsUnavailableLibrary)
        .join(', ');
  }
}

/// Uses add-only assignment endpoints. Partial successes stay selected as done;
/// retrying never replaces a user's unrelated assignments or routing defaults.
Future<void> assignRequestLibraries(
    BuildContext context, WidgetRef ref, List<int> userIds) async {
  if (userIds.isEmpty) return;
  final connection = ref.read(authProvider).valueOrNull?.connection;
  final instances = connection?.instances
          .where((i) => const ['radarr', 'sonarr', 'chaptarr', 'lidarr']
              .contains(i.serviceType))
          .toList() ??
      [];
  final selected = <String>{};
  final completed = <String>{};
  String? error;
  var busy = false;
  await showDialog<void>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
            builder: (dialogContext, update) => PopScope(
                canPop: !busy,
                child: AlertDialog(
                  title: Text(context.l10n.settingsAssignRequestLibraries),
                  content: SizedBox(
                      width: 480,
                      child: SingleChildScrollView(
                          child: Column(
                        mainAxisSize: MainAxisSize.min,
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(context.l10n
                              .settingsMessageAddLibrariesForValue1SelectedUsersExistingAssignments(
                                  (userIds.toSet().length).toString())),
                          if (connection?.instanceAssignments != true)
                            Text(context.l10n
                                .settingsUpdateTheServerToAssignLibrariesHere)
                          else if (instances.isEmpty)
                            Text(context.l10n
                                .settingsConnectRadarrSonarrChaptarrOrLidarrToOffer)
                          else
                            for (final instance in instances)
                              CheckboxListTile(
                                contentPadding: EdgeInsets.zero,
                                title: Text(instance.name),
                                subtitle: Text(completed.contains(instance.id)
                                    ? context.l10n.settingsAssigned
                                    : instance.serviceType),
                                value: selected.contains(instance.id),
                                onChanged:
                                    busy || completed.contains(instance.id)
                                        ? null
                                        : (value) => update(() {
                                              if (value == true) {
                                                selected.add(instance.id);
                                              } else {
                                                selected.remove(instance.id);
                                              }
                                            }),
                              ),
                          if (error != null)
                            Text(error!,
                                style: const TextStyle(color: AppTheme.error)),
                        ],
                      ))),
                  actions: [
                    TextButton(
                        onPressed:
                            busy ? null : () => Navigator.pop(dialogContext),
                        child: Text(context.l10n.settingsDone)),
                    FilledButton(
                        onPressed: busy ||
                                connection?.instanceAssignments != true ||
                                selected.difference(completed).isEmpty
                            ? null
                            : () async {
                                update(() {
                                  busy = true;
                                  error = null;
                                });
                                final service = InstanceApiService(
                                    backendDio:
                                        ref.read(backendClientProvider));
                                try {
                                  for (final id in selected
                                      .difference(completed)
                                      .toList()) {
                                    await service.changeAssignments(
                                        id, userIds.toSet(),
                                        add: true);
                                    completed.add(id);
                                  }
                                } on DioException {
                                  error = context.l10n
                                      .settingsSomeAssignmentsCouldNotBeConfirmedCompletedLibraries;
                                } catch (_) {
                                  error = context.l10n
                                      .settingsCouldNotFinishAssigningLibrariesRetryTheRemaining;
                                }
                                if (dialogContext.mounted) {
                                  update(() => busy = false);
                                }
                              },
                        child: Text(busy
                            ? context.l10n.settingsAssigning
                            : context.l10n.settingsAddSelectedLibraries)),
                  ],
                )),
          ));
}
