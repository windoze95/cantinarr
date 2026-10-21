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
      'radarr': 'Movies',
      'sonarr': 'TV',
      'chaptarr': 'Books',
      'lidarr': 'Music'
    };
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      _section('Cantinarr sign-in', [
        if (_status == null || _user == null)
          _message(
              'Could not verify Cantinarr sign-in settings. Refresh to retry.')
        else if (ssoRequired) ...[
          _message('Single sign-on required: ${_status!.ssoProvider}.'),
          if (!_status!.ssoAvailable)
            _message(
                'The single sign-on provider is unavailable. Review its settings before inviting this user.'),
          if (hasLink)
            _message(
                'Share the invitation. The recipient continues with ${_status!.ssoProvider} to join this account.'),
        ] else ...[
          if (_user!.hasPassword && _user!.passwordEnabled)
            _message('A Cantinarr password is configured.'),
          if (_user!.passkeyEnabled)
            _message(
                'Passkeys are allowed. The user must register a passkey before using it.'),
          if (_user!.ssoLinked)
            _message('A single sign-on identity is linked.'),
          if (hasLink)
            _message(
                'This invitation connects one device and expires in 7 days. The user can set up permitted sign-in methods after joining.'),
          if (!hasLink && _user!.hasPendingInvite)
            _message(
                'An invitation is outstanding. Its existing link is not shown again; use the account menu to replace it if needed.'),
          if (!hasLink &&
              !_user!.hasPendingInvite &&
              _user!.deviceCount == 0 &&
              !(_user!.hasPassword && _user!.passwordEnabled) &&
              !_user!.ssoLinked &&
              !(_plexLinked == true && _plex?['enabled'] == true))
            _message(
                'No current invitation or connected device. Issue an invitation to finish Cantinarr sign-in.'),
        ],
        if (hasPlex) ...[
          if (_plexLinked == null || _plex == null)
            _message(
                'Could not verify Plex sign-in. A media-account link alone does not confirm a sign-in identity.')
          else ...[
            _message(_plexLinked!
                ? 'Plex sign-in identity confirmed.'
                : 'No confirmed Plex sign-in identity.'),
            _message(_plex!['enabled'] == true
                ? 'Plex sign-in is enabled.'
                : 'Plex sign-in is off.'),
            _message(_plex!['auto_create'] == true
                ? 'Automatic signup is enabled for eligible Plex users.'
                : 'Automatic signup is off. Imported users do not need automatic signup.'),
            if (ssoRequired)
              _message(
                  'Plex does not satisfy the required single sign-on policy.'),
          ],
          TextButton(
            onPressed: () => _open('/settings/plex-auth'),
            child: const Text('Review Plex sign-in'),
          ),
        ],
        _message(
            'Jellyfin, Emby, and Audiobookshelf passwords are used on those servers. Importing an account does not make its password a Cantinarr password.'),
        Wrap(spacing: 8, runSpacing: 8, children: [
          if (!hasLink && _user != null)
            TextButton(
              onPressed: widget.onIssueInvitation ??
                  () => _open('/settings/users/${widget.userId}/access'),
              child: Text(_user!.hasPendingInvite
                  ? 'Replace invitation'
                  : 'Issue invitation'),
            ),
          TextButton(
              onPressed: () => _open('/settings/users/${widget.userId}/sso'),
              child: const Text('Linked sign-in')),
          if (hasLink)
            TextButton.icon(
              onPressed: () async {
                await Clipboard.setData(
                    ClipboardData(text: widget.invitationLink!));
                if (context.mounted) {
                  ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(content: Text('Invitation copied')));
                }
              },
              icon: const Icon(Icons.copy, size: 18),
              label: const Text('Copy invitation'),
            ),
        ]),
        if (hasLink && widget.originSource == 'app')
          _message(
              'This invitation uses the address this app connects to. For people outside your network, check External Address before sharing it.'),
      ]),
      _section('Request permissions', [
        if (_grants == null)
          _message('Could not load request assignments. Refresh to retry.')
        else ...[
          for (final type in requestTypes.entries)
            _message('${type.value}: ${_assignmentNames(type.key, instances)}'),
          _message(
              'No request assignments is a valid choice. Library playback access is configured separately.'),
        ],
        if (_user != null &&
            !_user!.isAdmin &&
            !_user!.permissions.contains('media:request'))
          _message('This account does not have permission to request content.'),
        Wrap(spacing: 8, runSpacing: 8, children: [
          TextButton(
              onPressed: () async {
                await assignRequestLibraries(context, ref, [widget.userId]);
                if (mounted) await _load();
              },
              child: const Text('Assign request libraries')),
          TextButton(
              onPressed: () => _open(
                  '/settings/users/${widget.userId}/request-settings${_user?.isAdmin == true ? '?admin=1' : ''}'),
              child: const Text('Request settings')),
        ]),
      ]),
      if (widget.showMediaAccounts)
        _section('Media server accounts', [
          if (_accounts == null)
            _message(
                'Could not verify media-server accounts. Refresh to retry.')
          else ...[
            if (mediaServers.isEmpty && _accounts!.isEmpty)
              _message('No media servers are configured.'),
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
          _message(
              'Linked accounts and active server access are separate. These statuses do not prove a title can be played.'),
        ]),
      TextButton.icon(
          onPressed: _load,
          icon: const Icon(Icons.refresh),
          label: const Text('Refresh access status')),
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
                ? 'No account linked'
                : '${account.remoteUsername}\n${account.managementLabel}\n${account.accessLabel}${account.granted ? '' : '\nNo Cantinarr library grant'}'),
            isThreeLine: account != null,
          ),
          if (showActions && widget.mediaAccountActions != null)
            widget.mediaAccountActions!(server, account),
        ]),
      );

  String _assignmentNames(String type, List<ServiceInstance> instances) {
    final ids = _grants![type] ?? const [];
    if (ids.isEmpty) return 'No libraries assigned';
    return ids
        .map((id) =>
            instances.where((i) => i.id == id).firstOrNull?.name ??
            'Unavailable library')
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
                  title: const Text('Assign request libraries'),
                  content: SizedBox(
                      width: 480,
                      child: SingleChildScrollView(
                          child: Column(
                        mainAxisSize: MainAxisSize.min,
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                              'Add libraries for ${userIds.toSet().length} selected users. Existing assignments and defaults stay in place.'),
                          if (connection?.instanceAssignments != true)
                            const Text(
                                'Update the server to assign libraries here.')
                          else if (instances.isEmpty)
                            const Text(
                                'Connect Radarr, Sonarr, Chaptarr, or Lidarr to offer requests.')
                          else
                            for (final instance in instances)
                              CheckboxListTile(
                                contentPadding: EdgeInsets.zero,
                                title: Text(instance.name),
                                subtitle: Text(completed.contains(instance.id)
                                    ? 'Assigned'
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
                        child: const Text('Done')),
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
                                  error =
                                      'Some assignments could not be confirmed. Completed libraries remain assigned; retry the remaining libraries.';
                                } catch (_) {
                                  error =
                                      'Could not finish assigning libraries. Retry the remaining libraries.';
                                }
                                if (dialogContext.mounted) {
                                  update(() => busy = false);
                                }
                              },
                        child: Text(
                            busy ? 'Assigning…' : 'Add selected libraries')),
                  ],
                )),
          ));
}
