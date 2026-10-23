import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/instance_api_service.dart';

/// Applies changes to a selection, never replaces a filtered directory.
class InstanceUsersScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final String instanceName;
  const InstanceUsersScreen(
      {super.key, required this.instanceId, required this.instanceName});
  @override
  ConsumerState<InstanceUsersScreen> createState() =>
      _InstanceUsersScreenState();
}

class _InstanceUsersScreenState extends ConsumerState<InstanceUsersScreen> {
  List<UserSummary> _users = [];
  Map<int, InstanceAssignment> _assignments = {};
  final Set<int> _selected = {};
  String _search = '';
  String _access = 'all';
  String _role = 'all';
  String _child = 'all';
  String _sso = 'all';
  String _invitation = 'all';
  bool _loading = true;
  bool _saving = false;
  String? _error;
  InstanceApiService get _service =>
      InstanceApiService(backendDio: ref.read(backendClientProvider));

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
      _selected.clear();
    });
    try {
      final users = await ref.read(authProvider.notifier).listUsers();
      final assignments = await _service.getAssignments(widget.instanceId);
      if (!mounted) return;
      setState(() {
        _users = users
          ..sort((a, b) =>
              a.username.toLowerCase().compareTo(b.username.toLowerCase()));
        _assignments = {for (final row in assignments) row.userId: row};
        _loading = false;
      });
    } catch (_) {
      if (mounted) {
        setState(() {
          _error = context.l10n.settingsCouldNotLoadUsersAndAssignmentsTryAgain;
          _loading = false;
        });
      }
    }
  }

  List<UserSummary> get _matching => _users.where((u) {
        final assigned = (_assignments[u.id]?.assigned ?? false);
        return u.username
                .toLowerCase()
                .contains(_search.trim().toLowerCase()) &&
            (_access == 'all' || assigned == (_access == 'assigned')) &&
            (_role == 'all' || u.role == _role) &&
            (_child == 'all' || u.child == (_child == 'yes')) &&
            (_sso == 'all' || u.ssoLinked == (_sso == 'yes')) &&
            (_invitation == 'all' ||
                u.hasPendingInvite == (_invitation == 'yes'));
      }).toList();

  String _instanceName(String id) {
    final instances =
        ref.read(authProvider).valueOrNull?.connection?.instances ?? [];
    for (final instance in instances) {
      if (instance.id == id) return instance.name;
    }
    return id.isEmpty
        ? context.l10n.settingsNone
        : context.l10n.settingsUnavailableInstance;
  }

  Future<void> _apply(bool add) async {
    final ids = _selected
        .where((id) => (_assignments[id]?.assigned ?? false) != add)
        .toList()
      ..sort();
    if (ids.isEmpty) return;
    if (!add) {
      final preferences = ids
          .where((id) =>
              _assignments[id]?.preferredInstanceId == widget.instanceId)
          .length;
      final confirmed = await showDialog<bool>(
          context: context,
          builder: (context) => AlertDialog(
                title: Text(context.l10n
                    .settingsMessageRemoveValue1Users((ids.length).toString())),
                content: Text(context.l10n
                    .settingsMessageRemoveValue1FromTheSelectedUsersAssignmentsAdministrators(
                        (widget.instanceName).toString(),
                        (preferences > 0
                                ? context.l10n
                                    .settingsMessageValue1SavedPreferencesWillBeClearedTheirDefault(
                                        (preferences).toString())
                                : '')
                            .toString())),
                actions: [
                  TextButton(
                      onPressed: () => Navigator.pop(context, false),
                      child: Text(context.l10n.settingsCancel)),
                  TextButton(
                      onPressed: () => Navigator.pop(context, true),
                      child: Text(context.l10n.settingsRemoveUsers))
                ],
              ));
      if (confirmed != true) return;
    }
    if (!mounted) return;
    setState(() => _saving = true);
    try {
      await _service.changeAssignments(widget.instanceId, ids, add: add);
      await ref.read(authProvider.notifier).refreshConfig();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text(context.l10n.settingsMessageValue1Value2Users(
              (add ? context.l10n.settingsAdded : context.l10n.settingsRemoved)
                  .toString(),
              (ids.length).toString()))));
      await _load();
    } catch (_) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
            content: Text(context
                .l10n.settingsCouldNotUpdateAssignmentsReloadAndTryAgain)));
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  Widget _filter(String label, String value, Map<String, String> options,
          void Function(String) update) =>
      SizedBox(
        width: 260,
        child: DropdownButtonFormField<String>(
          initialValue: value,
          isExpanded: true,
          decoration: InputDecoration(labelText: label),
          items: options.entries
              .map((e) => DropdownMenuItem(
                  value: e.key,
                  child: Text(e.value, overflow: TextOverflow.ellipsis)))
              .toList(),
          onChanged: _saving
              ? null
              : (v) {
                  if (v != null) {
                    setState(() {
                      update(v);
                      _selected.clear();
                    });
                  }
                },
        ),
      );

  Widget _userRow(UserSummary user) {
    final assigned = _assignments[user.id]?.assigned == true;
    final colors = Theme.of(context).colorScheme;
    final status = assigned
        ? context.l10n.settingsAssigned
        : context.l10n.settingsUnassigned;
    final color = assigned ? colors.tertiary : colors.onSurfaceVariant;
    void toggleSelection() => setState(() {
          if (!_selected.add(user.id)) _selected.remove(user.id);
        });

    return ListTile(
      key: ValueKey('assignment-${user.id}'),
      contentPadding: const EdgeInsets.symmetric(vertical: 8),
      leading: Tooltip(
        message: context.l10n.settingsMessageSelectValue1ForBulkChanges(
            (user.username).toString()),
        child: Checkbox(
          semanticLabel: context.l10n.settingsMessageSelectValue1ForBulkChanges(
              (user.username).toString()),
          value: _selected.contains(user.id),
          onChanged: _saving ? null : (_) => toggleSelection(),
        ),
      ),
      title: Wrap(
          spacing: 12,
          runSpacing: 8,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Text(user.username,
                style: const TextStyle(fontWeight: FontWeight.w600)),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              decoration: BoxDecoration(
                  color: color.withValues(alpha: 0.1),
                  borderRadius: BorderRadius.circular(8)),
              child: Row(mainAxisSize: MainAxisSize.min, children: [
                Icon(
                    assigned
                        ? Icons.check_circle_outline
                        : Icons.remove_circle_outline,
                    size: 16,
                    color: color),
                const SizedBox(width: 6),
                Text(status,
                    style: Theme.of(context)
                        .textTheme
                        .labelMedium
                        ?.copyWith(color: color)),
              ]),
            ),
          ]),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 6),
        child: Text(context.l10n.settingsMessageValue1RequestDestinationValue2(
            (user.isAdmin
                    ? context.l10n.settingsAdministratorCanBrowseAllInstancesN
                    : '')
                .toString(),
            (_instanceName(_assignments[user.id]?.effectiveDefaultId ?? ''))
                .toString())),
      ),
      onTap: _saving ? null : toggleSelection,
    );
  }

  @override
  Widget build(BuildContext context) {
    final matching = _matching;
    final addCount =
        _selected.where((id) => !(_assignments[id]?.assigned ?? false)).length;
    final removeCount = _selected.length - addCount;
    final assignedCount =
        matching.where((u) => _assignments[u.id]?.assigned == true).length;
    final adminCount = matching.where((u) => u.isAdmin).length;
    final unassignedCount = matching.length - assignedCount;
    return Scaffold(
      appBar: AppBar(
          title: Text(context.l10n.settingsMessageManageUsersValue1(
              (widget.instanceName).toString()))),
      body: CenteredContent(
          child: _loading
              ? const Center(child: CircularProgressIndicator())
              : _error != null
                  ? Center(
                      child: Column(mainAxisSize: MainAxisSize.min, children: [
                      Text(_error!),
                      TextButton(
                          onPressed: _load,
                          child: Text(context.l10n.settingsRetry))
                    ]))
                  : ListView(padding: const EdgeInsets.all(16), children: [
                      TextField(
                          decoration: InputDecoration(
                              labelText: context.l10n.settingsSearchUsers,
                              prefixIcon: Icon(Icons.search)),
                          enabled: !_saving,
                          onChanged: (v) => setState(() {
                                _search = v;
                                _selected.clear();
                              })),
                      const SizedBox(height: 12),
                      Wrap(spacing: 16, runSpacing: 12, children: [
                        _filter(
                            context.l10n.settingsAccess,
                            _access,
                            {
                              'all': context.l10n.settingsAllUsers,
                              'assigned': context.l10n.settingsAssigned,
                              'unassigned': context.l10n.settingsUnassigned
                            },
                            (v) => _access = v),
                        _filter(
                            context.l10n.settingsRole,
                            _role,
                            {
                              'all': context.l10n.settingsAllRoles,
                              'user': context.l10n.settingsUser,
                              'admin': context.l10n.settingsAdministrator
                            },
                            (v) => _role = v),
                        _filter(
                            context.l10n.settingsChildAccount,
                            _child,
                            {
                              'all': context.l10n.settingsAllAccounts,
                              'yes': context.l10n.settingsChildren,
                              'no': context.l10n.settingsAdults
                            },
                            (v) => _child = v),
                        _filter(
                            context.l10n.settingsSsoLinked,
                            _sso,
                            {
                              'all': context.l10n.settingsAllAccounts,
                              'yes': context.l10n.settingsLinked,
                              'no': context.l10n.settingsNotLinked
                            },
                            (v) => _sso = v),
                        _filter(
                            context.l10n.settingsInvitation,
                            _invitation,
                            {
                              'all': context.l10n.settingsAllAccounts,
                              'yes': context.l10n.settingsPendingInvitation,
                              'no': context.l10n.settingsNoPendingInvitation
                            },
                            (v) => _invitation = v),
                      ]),
                      const SizedBox(height: 16),
                      Text(
                          context.l10n
                              .settingsMessagePersonalAssignmentsToValue1(
                                  (widget.instanceName).toString()),
                          style: Theme.of(context).textTheme.titleMedium),
                      const SizedBox(height: 4),
                      Text(context.l10n
                          .settingsMessageValue1AssignedValue2UnassignedValue3Value4InThese(
                              (assignedCount).toString(),
                              (unassignedCount).toString(),
                              (adminCount).toString(),
                              (adminCount == 1
                                      ? context.l10n.settingsAdministrator2
                                      : context.l10n.settingsAdministrators)
                                  .toString())),
                      const SizedBox(height: 12),
                      Text(context.l10n
                          .settingsSelectUsersForBulkChangesCheckboxesSelectRows),
                      const SizedBox(height: 8),
                      Text(context.l10n
                          .settingsMessageValue1MatchingValue2Selected(
                              (matching.length).toString(),
                              (_selected.length).toString())),
                      Wrap(spacing: 8, children: [
                        TextButton(
                            onPressed: _saving
                                ? null
                                : () => setState(() {
                                      _selected
                                          .addAll(matching.map((u) => u.id));
                                    }),
                            child:
                                Text(context.l10n.settingsSelectAllMatching)),
                        TextButton(
                            onPressed: _saving
                                ? null
                                : () => setState(_selected.clear),
                            child: Text(context.l10n.settingsClearSelection)),
                        FilledButton(
                            onPressed: _saving || addCount == 0
                                ? null
                                : () => _apply(true),
                            child: Text(context.l10n
                                .settingsMessageAddSelectedValue1(
                                    (addCount).toString()))),
                        OutlinedButton(
                            onPressed: _saving || removeCount == 0
                                ? null
                                : () => _apply(false),
                            child: Text(context.l10n
                                .settingsMessageRemoveSelectedValue1(
                                    (removeCount).toString()))),
                      ]),
                      if (_saving) const LinearProgressIndicator(),
                      if (matching.isEmpty)
                        Padding(
                            padding: EdgeInsets.all(24),
                            child: Text(
                                context.l10n.settingsNoUsersMatchTheseFilters)),
                      for (final user in matching) _userRow(user),
                    ])),
    );
  }
}
