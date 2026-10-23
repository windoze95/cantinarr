import 'media_access_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
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
        setState(() => _startError = context.l10n
            .mediaThisMediaServerIsNoLongerConfiguredChooseAnotherServerOrConnectOne);
      } else if (requested != null || servers.length == 1) {
        _chooseServer(requested ?? servers.single);
      }
    } catch (_) {
      if (mounted) {
        setState(() {
          _starting = false;
          _startError = context.l10n.mediaCouldNotLoadTheConnectedMediaServers;
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
            context.l10n.mediaChoosePlexJellyfinEmbyOrAudiobookshelf,
        'refresh_config_after_return': true,
      });
      if (!mounted) return;
      await ref.read(authProvider.notifier).refreshConfig();
      if (mounted) await _start();
    } catch (_) {
      if (mounted) {
        setState(() => _startError = context.l10n
            .mediaCouldNotRefreshTheConnectedMediaServersTheirLatestConfigurationIsUnknown);
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
            title: Text(product == null
                ? context.l10n.mediaImportUsers
                : context.l10n.mediaImportValue0Users((product).toString()))),
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
              Text(context.l10n.mediaChooseAMediaServer,
                  style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: AppTheme.spaceSm),
              Text(context.l10n
                  .mediaImportAccountsFromPlexJellyfinEmbyOrAudiobookshelfChooseWhichAccountsGetANewCantinarrUserA),
              const SizedBox(height: AppTheme.spaceLg),
              if (servers.isEmpty) ...[
                Text(context
                    .l10n.mediaConnectAMediaServerBeforeImportingItsUsers),
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
                  label: Text(context.l10n.mediaConnectAMediaServer)),
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
                  child: Text(context.l10n
                      .mediaValue0ReportedNoAccountsToImport(
                          (controller.server.name).toString()))))
        else ...[
          SliverPadding(
              padding: EdgeInsets.fromLTRB(padding, 16, padding, 8),
              sliver: SliverToBoxAdapter(
                  child: Column(children: [
                TextField(
                  controller: _search,
                  decoration: InputDecoration(
                      labelText: context.l10n.mediaSearchAccounts,
                      prefixIcon: Icon(Icons.search)),
                  onChanged: (_) => setState(() {}),
                ),
                Row(children: [
                  Expanded(
                      child: Text(context.l10n.mediaValue0ToImportValue1Maximum(
                          (controller.selectedIncompleteCount).toString(),
                          (MediaUserImportController.maxSelection)
                              .toString()))),
                  TextButton(
                      onPressed: controller.busy || controller.reconciling
                          ? null
                          : () => controller.selectAll(visible),
                      child: Text(context.l10n.mediaSelectAll)),
                  TextButton(
                      onPressed: controller.busy || controller.reconciling
                          ? null
                          : controller.clearSelection,
                      child: Text(context.l10n.mediaClear)),
                ]),
                if (visible.isEmpty)
                  Text(context.l10n.mediaNoAccountsMatchThisSearch),
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
                    ? context.l10n.mediaImportingAccounts
                    : controller.successfulUserIds.isNotEmpty &&
                            controller.selectedIncompleteCount == 0
                        ? context.l10n.mediaSelectedAccountsImported
                        : controller.selected
                                .any((c) => !c.complete && c.result != null)
                            ? context.l10n.mediaRetrySelectedIncompleteAccounts
                            : context.l10n.mediaImportSelectedAccounts),
              ),
              if (controller.successfulUserIds.isNotEmpty)
                OutlinedButton.icon(
                  onPressed: _assigning ||
                          controller.busy ||
                          controller.reconciling ||
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
                  label: Text(context.l10n.mediaAssignRequestLibraries),
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
      Text(controller.submitted
          ? context.l10n.mediaResultsAndNextSteps
          : context.l10n.mediaChooseAccounts),
      const SizedBox(height: 8),
      if (controller.successfulUserIds.isNotEmpty) ...[
        Text(context.l10n
            .mediaReviewCantinarrSignInRequestPermissionsAndMediaAccessBelowUseAssignRequestLibrariesForChec),
        const SizedBox(height: 8),
      ],
      Text(context.l10n
          .mediaImportingLinksAccountsFromValue0ToCantinarrUsersItDoesNotConfirmThatSignInRequestsOrPlayba(
              (product).toString())),
      if (product != 'Plex')
        Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(context.l10n
                .mediaTheirValue0PasswordDoesNotBecomeACantinarrPassword(
                    (product).toString()))),
      if (!controller.supportsExplicitLinking)
        _message(context.l10n
            .mediaUpdateTheCantinarrServerToImportUsersSafelyWithExplicitAccountLinkingThisAppWillNotAutomat),
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
                label: Text(context.l10n.mediaChooseAnotherServer))),
      if (controller.supportsManagement)
        ExpansionTile(
          tilePadding: EdgeInsets.zero,
          title: Text(context.l10n.mediaRemoteAccessManagement),
          subtitle: Text(controller.manageAccess
              ? context.l10n.mediaManageSelectedAccounts
              : context.l10n.mediaOffPreserveExistingServerAccess),
          children: [
            CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              value: controller.manageAccess,
              onChanged: controller.busy || controller.submitted
                  ? null
                  : (value) => controller.setManagement(value == true),
              title: Text(
                  context.l10n.mediaManageImportedAccountsThroughCantinarr),
              subtitle: Text(context.l10n
                  .mediaEnablesTheseAccountsNowRemovingTheirCantinarrGrantOrDeletingTheUserWillTurnOffServerAccess),
              controlAffinity: ListTileControlAffinity.leading,
            )
          ],
        ),
      if (controller.error != null) ...[
        _message(mediaAccessLabel(context, controller.error!), error: true),
        TextButton(
            onPressed: controller.busy || controller.reconciling
                ? null
                : controller.submitted
                    ? controller.reconcile
                    : controller.load,
            child: Text(controller.submitted
                ? context.l10n.mediaCheckCurrentState
                : context.l10n.mediaRefreshAccounts)),
      ] else if (controller.submitted)
        TextButton.icon(
            onPressed: controller.busy || controller.reconciling
                ? null
                : controller.reconcile,
            icon: const Icon(Icons.refresh),
            label: Text(context.l10n.mediaCheckCurrentState)),
    ]);
  }

  Widget _account(
      MediaUserImportController controller, MediaImportChoice choice) {
    final remote = choice.remote;
    final result = choice.result;
    final linkedOwner = choice.account == null
        ? null
        : _ownerName(context, controller, choice.account!.userId);
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
                    if (!choice.listed)
                      context.l10n.mediaNoLongerListedByTheMediaServer,
                    if (choice.mappingUnavailable)
                      context.l10n
                          .mediaTheSelectedCantinarrUserIsNoLongerAvailableChooseAnotherUser,
                    if (remote.isAdministrator)
                      context.l10n.mediaProtectedAdministrator,
                    if (remote.isDisabled &&
                        choice.account == null &&
                        result == null)
                      controller.submitted
                          ? context.l10n.mediaLastListedAsDisabled
                          : context.l10n.mediaDisabledOnServer,
                    if (remote.pending &&
                        choice.account == null &&
                        result == null)
                      controller.submitted
                          ? context.l10n.mediaLastListedWithAPendingInvitation
                          : context.l10n.mediaAwaitingInvitationAcceptance,
                    if (choice.alreadyLinked)
                      controller.accountsCurrent
                          ? context.l10n.mediaAlreadyLinkedToValue0(
                              (linkedOwner).toString())
                          : context.l10n.mediaLastKnownLinkToValue0(
                              (linkedOwner).toString()),
                    if (choice.complete)
                      context.l10n
                          .mediaImportedAsValue0((result!.username).toString()),
                    if (!controller.accountsCurrent &&
                        (choice.account != null || result != null))
                      context.l10n.mediaServerAccessUnconfirmed,
                    if (controller.accountsCurrent && choice.account != null)
                      mediaAccessLabel(context, choice.account!.accessLabel),
                    if (controller.accountsCurrent &&
                        choice.complete &&
                        choice.account == null)
                      context.l10n.mediaNoCurrentMediaAccountLink,
                    if (!choice.alreadyLinked && !choice.complete)
                      choice.targetUserId != null
                          ? context.l10n.mediaLinkToValue0((_ownerName(
                                  context, controller, choice.targetUserId!))
                              .toString())
                          : choice.requiresMapping
                              ? context.l10n
                                  .mediaUsernameExistsChooseItsUserExplicitly
                              : context.l10n.mediaCreateANewCantinarrUser,
                  ].join(' · '),
                  style: const TextStyle(color: AppTheme.textSecondary)),
              value: choice.complete
                  ? !_excludedAssignments.contains(result!.userId)
                  : choice.selected,
              onChanged: controller.busy || controller.reconciling
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
              if (result?.userId != null)
                Text(result!.created
                    ? context.l10n
                        .mediaUserValue0WasCreatedRetryingCompletesTheLinkForThisUserAndKeepsTheirInvitation(
                            (result.username).toString())
                    : context.l10n
                        .mediaContinueLinkingToValue0RetryingKeepsThisSelectedCantinarrUser(
                            (result.username).toString()))
              else
                DropdownButtonFormField<int>(
                  key: ValueKey('mapping-${remote.id}-${choice.targetUserId}'),
                  initialValue: choice.targetUserId,
                  isExpanded: true,
                  decoration: InputDecoration(
                      labelText: context.l10n.mediaLinkToExistingUser),
                  hint: Text(choice.requiresMapping
                      ? context.l10n.mediaChooseACantinarrUser
                      : context.l10n.mediaCreateNewUserDefault),
                  items: [
                    if (!choice.requiresMapping)
                      DropdownMenuItem(
                          value: -1,
                          child: Text(context.l10n.mediaCreateNewUser)),
                    for (final user in controller.users)
                      DropdownMenuItem(
                          value: user.id,
                          child: Text(
                              '${user.username}${user.isAdmin ? ' (administrator)' : ''}')),
                  ],
                  onChanged: controller.busy || controller.reconciling
                      ? null
                      : (value) =>
                          controller.mapTo(choice, value == -1 ? null : value),
                ),
            if (choice.uncertain)
              _message(
                  context.l10n
                      .mediaOutcomeUnknownCheckCurrentStateBeforeRetryingThisAccount,
                  error: true),
            if (result != null && !choice.complete)
              _message(_outcome(result), error: true),
            if (result?.userId != null) ...[
              if (result!.plexIdentityError.isNotEmpty)
                _message(context
                    .l10n.mediaMediaAccountLinkedPlexSignInNeedsIdentityReview),
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
                  label: Text(context.l10n.mediaSignInAndAccessNextSteps)),
              if (_expanded.contains(remote.id))
                if (controller.busy || controller.reconciling)
                  Padding(
                    padding: EdgeInsets.all(16),
                    child: Text(
                        context.l10n.mediaCheckingCurrentSignInAndAccessStatus),
                  )
                else
                  UserOnboardingStatus(
                    key: ValueKey('status-${remote.id}-$_statusVersion-'
                        '${controller.accessReadRevision}'),
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
                  child: Text(context.l10n.mediaOpenUserAccess)),
            ],
          ]),
        ));
  }

  String _outcome(MediaServerImportResult result) => switch (result.error) {
        'username_conflict' => context.l10n
            .mediaTheUsernameNowExistsSelectTheIntendedCantinarrUserExplicitly,
        'already_linked' =>
          context.l10n.mediaThisAccountIsNowLinkedToAnotherUserReviewItsOwner,
        'not_found' =>
          context.l10n.mediaThisAccountIsNoLongerListedOnTheMediaServer,
        'user_has_account' => context
            .l10n.mediaTheSelectedCantinarrUserAlreadyHasAnAccountOnThisServer,
        'user_failed' => context.l10n.mediaTheCantinarrUserCouldNotBeCreated,
        _ => context.l10n
            .mediaTheAccountLinkIsIncompleteSelectItToRetryOnlyTheUnfinishedStep,
      };

  Widget _message(String message, {bool error = false}) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: Text(message,
            style: TextStyle(
                color: error ? AppTheme.error : AppTheme.textSecondary)),
      );
}

String _ownerName(BuildContext context, MediaUserImportController controller,
        int userId) =>
    controller.users.where((user) => user.id == userId).firstOrNull?.username ??
    context.l10n.mediaUserNumber(userId);
