import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../../settings/ui/user_onboarding_status.dart';
import '../data/media_access_service.dart';
import '../data/media_user_import_controller.dart';

/// One routed import journey, shared by Users and each media server's settings.
class MediaUserImportScreen extends ConsumerStatefulWidget {
  final String? instanceId;
  const MediaUserImportScreen({super.key, this.instanceId});

  @override
  ConsumerState<MediaUserImportScreen> createState() =>
      _MediaUserImportScreenState();
}

class _MediaUserImportScreenState extends ConsumerState<MediaUserImportScreen> {
  MediaUserImportController? _controller;
  final _search = TextEditingController();
  final Set<String> _expanded = {};
  final Set<int> _excludedAssignments = {};
  bool _starting = true;
  bool _assigning = false;
  bool _openedInitialStatus = false;
  String? _startError;
  int _statusVersion = 0;

  @override
  void initState() {
    super.initState();
    _start();
  }

  Future<void> _start() async {
    try {
      final auth = await ref.read(authProvider.future);
      if (!mounted) return;
      var servers = auth.connection?.mediaServerInstances ?? [];
      var requested =
          servers.where((s) => s.id == widget.instanceId).firstOrNull;
      // A newly saved server can replace its settings route before the Users
      // route resumes and refreshes configuration. Re-read before declaring a
      // preselected server missing; a failed read is an unknown configuration.
      if (widget.instanceId != null && requested == null) {
        await ref.read(authProvider.notifier).refreshConfig();
        if (!mounted) return;
        servers = ref
                .read(authProvider)
                .valueOrNull
                ?.connection
                ?.mediaServerInstances ??
            [];
        requested = servers.where((s) => s.id == widget.instanceId).firstOrNull;
      }
      setState(() => _starting = false);
      if (widget.instanceId != null && requested == null) {
        setState(
            () => _startError = 'This media server is no longer configured. '
                'Choose another server or connect one.');
      } else if (requested != null || servers.length == 1) {
        _chooseServer(requested ?? servers.single);
      }
    } catch (_) {
      if (mounted) {
        setState(() {
          _starting = false;
          _startError = 'Could not load the connected media servers.';
        });
      }
    }
  }

  void _chooseServer(ServiceInstance server) {
    final connection = ref.read(authProvider).valueOrNull?.connection;
    if (connection == null) return;
    _controller?.removeListener(_update);
    _controller?.dispose();
    _controller = MediaUserImportController(
      service: ref.read(mediaAccessServiceProvider),
      readUsers: () => ref.read(authProvider.notifier).listUsers(),
      server: server,
      serverUrl: connection.serverUrl,
      supportsExplicitLinking: connection.mediaUserImportExplicitLinking,
      supportsManagement: connection.mediaAccountManagement,
    )..addListener(_update);
    _search.clear();
    _expanded.clear();
    _excludedAssignments.clear();
    _openedInitialStatus = false;
    setState(() => _startError = null);
    _controller!.load();
  }

  void _update() {
    if (!_openedInitialStatus) {
      final firstSuccess =
          _controller?.choices.where((c) => c.complete).firstOrNull;
      if (firstSuccess != null) {
        _expanded.add(firstSuccess.remote.id);
        _openedInitialStatus = true;
      }
    }
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _controller?.removeListener(_update);
    _controller?.dispose();
    _search.dispose();
    super.dispose();
  }

  Future<void> _connectServer() async {
    try {
      await context.push('/settings/instance/new', extra: {
        'service_type': 'plex',
        'service_type_prompt':
            'Choose Plex, Jellyfin, Emby, or Audiobookshelf.',
        'refresh_config_after_return': true,
      });
      if (!mounted) return;
      await ref.read(authProvider.notifier).refreshConfig();
      if (mounted) await _start();
    } catch (_) {
      if (mounted) {
        setState(() =>
            _startError = 'Could not refresh the connected media servers. '
                'Their latest configuration is unknown.');
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final controller = _controller;
    final product = controller == null
        ? null
        : mediaServerTypeLabel(controller.server.serviceType);
    final servers =
        ref.watch(authProvider).valueOrNull?.connection?.mediaServerInstances ??
            const <ServiceInstance>[];
    return PopScope(
      canPop: !(controller?.busy ?? false) && !_assigning,
      child: Scaffold(
        backgroundColor: Colors.transparent,
        appBar: AppBar(
            title: Text(
                product == null ? 'Import users' : 'Import $product users')),
        body: _starting
            ? const Center(child: CircularProgressIndicator())
            : controller == null
                ? _serverPicker(servers)
                : _accounts(controller),
      ),
    );
  }

  Widget _serverPicker(List<ServiceInstance> servers) => ListView(
        padding: const EdgeInsets.all(AppTheme.spaceLg),
        children: [
          CenteredContent(
              child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (_startError != null) _message(_startError!, error: true),
              Text('Choose a media server',
                  style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: AppTheme.spaceSm),
              const Text(
                  'Import accounts from Plex, Jellyfin, Emby, or Audiobookshelf. '
                  'Choose which accounts get a new Cantinarr user and which belong '
                  'to an existing user.'),
              const SizedBox(height: AppTheme.spaceLg),
              if (servers.isEmpty) ...[
                const Text(
                    'Connect a media server before importing its users.'),
                const SizedBox(height: AppTheme.spaceMd),
              ],
              for (final server in servers)
                Card(
                    child: ListTile(
                  title: Text(server.name),
                  subtitle: Text(mediaServerTypeLabel(server.serviceType)),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: () => _chooseServer(server),
                )),
              OutlinedButton.icon(
                  onPressed: _connectServer,
                  icon: const Icon(Icons.add),
                  label: const Text('Connect a media server')),
            ],
          ))
        ],
      );

  Widget _accounts(MediaUserImportController controller) {
    final query = _search.text.trim().toLowerCase();
    final visible = controller.choices
        .where((choice) =>
            query.isEmpty ||
            choice.username.toLowerCase().contains(query) ||
            choice.remote.id.toLowerCase().contains(query))
        .toList();
    final padding =
        AppBreakpoints.centeredContentPadding(MediaQuery.sizeOf(context).width);
    return Column(children: [
      Expanded(
          child: CustomScrollView(slivers: [
        SliverPadding(
          padding: EdgeInsets.fromLTRB(padding, 16, padding, 0),
          sliver: SliverToBoxAdapter(child: _header(controller)),
        ),
        if (controller.loading)
          const SliverToBoxAdapter(
              child: Padding(
            padding: EdgeInsets.all(32),
            child: Center(child: CircularProgressIndicator()),
          ))
        else if (controller.choices.isEmpty && controller.error == null)
          SliverPadding(
              padding: EdgeInsets.all(padding),
              sliver: SliverToBoxAdapter(
                  child: Text(
                      '${controller.server.name} reported no accounts to import.')))
        else ...[
          SliverPadding(
              padding: EdgeInsets.fromLTRB(padding, 16, padding, 8),
              sliver: SliverToBoxAdapter(
                  child: Column(children: [
                TextField(
                  controller: _search,
                  decoration: const InputDecoration(
                      labelText: 'Search accounts',
                      prefixIcon: Icon(Icons.search)),
                  onChanged: (_) => setState(() {}),
                ),
                Row(children: [
                  Expanded(
                      child: Text(
                          '${controller.selectedIncompleteCount} to import · '
                          '${MediaUserImportController.maxSelection} maximum')),
                  TextButton(
                      onPressed: controller.busy
                          ? null
                          : () => controller.selectAll(visible),
                      child: const Text('Select all')),
                  TextButton(
                      onPressed:
                          controller.busy ? null : controller.clearSelection,
                      child: const Text('Clear')),
                ]),
                if (visible.isEmpty)
                  const Text('No accounts match this search.'),
              ]))),
          SliverPadding(
              padding: EdgeInsets.symmetric(horizontal: padding),
              sliver: SliverList.builder(
                  itemCount: visible.length,
                  itemBuilder: (context, index) =>
                      _account(controller, visible[index]))),
        ],
        const SliverToBoxAdapter(child: SizedBox(height: 24)),
      ])),
      SafeArea(
          top: false,
          child: Padding(
            padding: EdgeInsets.fromLTRB(padding, 8, padding, 12),
            child: Wrap(spacing: 12, runSpacing: 8, children: [
              FilledButton.icon(
                onPressed: controller.canSubmit ? controller.submit : null,
                icon: controller.busy
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.person_add_alt_1),
                label: Text(controller.busy
                    ? 'Importing accounts…'
                    : controller.successfulUserIds.isNotEmpty &&
                            controller.selectedIncompleteCount == 0
                        ? 'Selected accounts imported'
                        : controller.selected
                                .any((c) => !c.complete && c.result != null)
                            ? 'Retry selected incomplete accounts'
                            : 'Import selected accounts'),
              ),
              if (controller.successfulUserIds.isNotEmpty)
                OutlinedButton.icon(
                  onPressed: _assigning ||
                          controller.busy ||
                          controller.successfulUserIds
                              .every(_excludedAssignments.contains)
                      ? null
                      : () async {
                          setState(() => _assigning = true);
                          await assignRequestLibraries(
                              context,
                              ref,
                              controller.successfulUserIds
                                  .where((id) =>
                                      !_excludedAssignments.contains(id))
                                  .toList());
                          if (mounted) {
                            setState(() {
                              _assigning = false;
                              _statusVersion++;
                            });
                          }
                        },
                  icon: const Icon(Icons.video_library_outlined),
                  label: const Text('Assign request libraries'),
                ),
            ]),
          )),
    ]);
  }

  Widget _header(MediaUserImportController controller) {
    final product = mediaServerTypeLabel(controller.server.serviceType);
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(controller.server.name,
          style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 8),
      Text(controller.submitted ? 'Results and next steps' : 'Choose accounts'),
      const SizedBox(height: 8),
      if (controller.successfulUserIds.isNotEmpty) ...[
        const Text('Review Cantinarr sign-in, request permissions, and media '
            'access below. Use Assign request libraries for checked users.'),
        const SizedBox(height: 8),
      ],
      Text('Importing links accounts from $product to Cantinarr users. '
          'It does not confirm that sign-in, requests, or playback are ready. '
          'Existing users must be selected explicitly.'),
      if (product != 'Plex')
        Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(
                'Their $product password does not become a Cantinarr password.')),
      if (!controller.supportsExplicitLinking)
        _message(
            'Update the Cantinarr server to import users safely with explicit '
            'account linking. This app will not automatically match existing usernames.'),
      if (!controller.submitted)
        Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
                onPressed: controller.busy
                    ? null
                    : () => setState(() {
                          _controller?.removeListener(_update);
                          _controller?.dispose();
                          _controller = null;
                        }),
                icon: const Icon(Icons.swap_horiz),
                label: const Text('Choose another server'))),
      if (controller.supportsManagement)
        ExpansionTile(
          tilePadding: EdgeInsets.zero,
          title: const Text('Remote access management'),
          subtitle: Text(controller.manageAccess
              ? 'Manage selected accounts'
              : 'Off: preserve existing server access'),
          children: [
            CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              value: controller.manageAccess,
              onChanged: controller.busy || controller.submitted
                  ? null
                  : (value) => controller.setManagement(value == true),
              title: const Text('Manage imported accounts through Cantinarr'),
              subtitle: const Text('Enables these accounts now. Removing their '
                  'Cantinarr grant or deleting the user will turn off server access. '
                  'Existing libraries stay the same. Administrators are protected.'),
              controlAffinity: ListTileControlAffinity.leading,
            )
          ],
        ),
      if (controller.error != null) ...[
        _message(controller.error!, error: true),
        TextButton(
            onPressed: controller.busy
                ? null
                : controller.submitted
                    ? controller.reconcile
                    : controller.load,
            child: Text(controller.submitted
                ? 'Check current state'
                : 'Refresh accounts')),
      ] else if (controller.submitted)
        TextButton.icon(
            onPressed: controller.busy ? null : controller.reconcile,
            icon: const Icon(Icons.refresh),
            label: const Text('Check current state')),
    ]);
  }

  Widget _account(
      MediaUserImportController controller, MediaImportChoice choice) {
    final remote = choice.remote;
    final result = choice.result;
    final linkedOwner = choice.account == null
        ? null
        : controller.ownerName(choice.account!.userId);
    return Card(
        key: ValueKey('account-${remote.id}'),
        child: Padding(
          padding: const EdgeInsets.all(12),
          child:
              Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              controlAffinity: ListTileControlAffinity.leading,
              title: Text(choice.username,
                  style: const TextStyle(color: AppTheme.textPrimary)),
              subtitle: Text(
                  [
                    if (!choice.listed) 'No longer listed by the media server',
                    if (remote.isAdministrator) 'Protected administrator',
                    if (remote.isDisabled) 'Disabled on server',
                    if (remote.pending) 'Awaiting invitation acceptance',
                    if (choice.alreadyLinked) 'Already linked to $linkedOwner',
                    if (choice.complete) 'Linked to ${result!.username}',
                    if (choice.account != null) choice.account!.accessLabel,
                    if (!choice.alreadyLinked && !choice.complete)
                      choice.targetUserId != null
                          ? 'Link to ${controller.ownerName(choice.targetUserId!)}'
                          : choice.requiresMapping
                              ? 'Username exists. Choose its user explicitly.'
                              : 'Create a new Cantinarr user',
                  ].join(' · '),
                  style: const TextStyle(color: AppTheme.textSecondary)),
              value: choice.complete
                  ? !_excludedAssignments.contains(result!.userId)
                  : choice.selected,
              onChanged: controller.busy
                  ? null
                  : choice.complete
                      ? (value) {
                          setState(() {
                            if (value == true) {
                              _excludedAssignments.remove(result!.userId);
                            } else {
                              _excludedAssignments.add(result!.userId!);
                            }
                          });
                        }
                      : choice.selectable
                          ? (value) => controller.select(choice, value == true)
                          : null,
            ),
            if (!choice.complete && !choice.alreadyLinked && !choice.uncertain)
              if (result?.created == true)
                Text(
                    'User ${result!.username} was created. Retrying completes the '
                    'link for this user and keeps their invitation.')
              else
                DropdownButtonFormField<int>(
                  key: ValueKey('mapping-${remote.id}-${choice.targetUserId}'),
                  initialValue: choice.targetUserId,
                  isExpanded: true,
                  decoration:
                      const InputDecoration(labelText: 'Link to existing user'),
                  hint: Text(choice.requiresMapping
                      ? 'Choose a Cantinarr user'
                      : 'Create new user (default)'),
                  items: [
                    if (!choice.requiresMapping)
                      const DropdownMenuItem(
                          value: -1, child: Text('Create new user')),
                    for (final user in controller.users)
                      DropdownMenuItem(
                          value: user.id,
                          child: Text(
                              '${user.username}${user.isAdmin ? ' (administrator)' : ''}')),
                  ],
                  onChanged: controller.busy
                      ? null
                      : (value) =>
                          controller.mapTo(choice, value == -1 ? null : value),
                ),
            if (choice.uncertain)
              _message(
                  'Outcome unknown. Check current state '
                  'before retrying this account.',
                  error: true),
            if (result != null && !choice.complete)
              _message(_outcome(result), error: true),
            if (result?.userId != null) ...[
              if (result!.plexIdentityError.isNotEmpty)
                _message(
                    'Media account linked. Plex sign-in needs identity review.'),
              const SizedBox(height: 4),
              TextButton.icon(
                  onPressed: () => setState(() {
                        if (!_expanded.add(remote.id)) {
                          _expanded.remove(remote.id);
                        }
                      }),
                  icon: Icon(_expanded.contains(remote.id)
                      ? Icons.expand_less
                      : Icons.expand_more),
                  label: const Text('Sign-in and access next steps')),
              if (_expanded.contains(remote.id))
                UserOnboardingStatus(
                  key: ValueKey('status-${remote.id}-$_statusVersion'),
                  userId: result.userId!,
                  invitationLink: result.link,
                  originSource: result.originSource,
                ),
              TextButton(
                  onPressed: () async {
                    await context
                        .push('/settings/users/${result.userId}/access');
                    if (!mounted) return;
                    await controller.reconcile();
                    if (mounted) setState(() => _statusVersion++);
                  },
                  child: const Text('Open user access')),
            ],
          ]),
        ));
  }

  String _outcome(MediaServerImportResult result) => switch (result.error) {
        'username_conflict' =>
          'The username now exists. Select the intended Cantinarr user explicitly.',
        'already_linked' =>
          'This account is now linked to another user. Review its owner.',
        'not_found' => 'This account is no longer listed on the media server.',
        'user_has_account' =>
          'The selected Cantinarr user already has an account on this server.',
        'user_failed' => 'The Cantinarr user could not be created.',
        _ =>
          'The account link is incomplete. Select it to retry only the unfinished step.',
      };

  Widget _message(String message, {bool error = false}) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: Text(message,
            style: TextStyle(
                color: error ? AppTheme.error : AppTheme.textSecondary)),
      );
}
