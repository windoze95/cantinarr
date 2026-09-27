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
  const InstanceUsersScreen({super.key, required this.instanceId, required this.instanceName});
  @override
  ConsumerState<InstanceUsersScreen> createState() => _InstanceUsersScreenState();
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
  InstanceApiService get _service => InstanceApiService(backendDio: ref.read(backendClientProvider));

  @override
  void initState() { super.initState(); _load(); }

  Future<void> _load() async {
    setState(() { _loading = true; _error = null; _selected.clear(); });
    try {
      final users = await ref.read(authProvider.notifier).listUsers();
      final assignments = await _service.getAssignments(widget.instanceId);
      if (!mounted) return;
      setState(() {
        _users = users..sort((a, b) => a.username.toLowerCase().compareTo(b.username.toLowerCase()));
        _assignments = {for (final row in assignments) row.userId: row};
        _loading = false;
      });
    } catch (_) {
      if (mounted) setState(() { _error = 'Could not load users and assignments. Try again.'; _loading = false; });
    }
  }

  List<UserSummary> get _matching => _users.where((u) {
    final assigned = u.isAdmin || (_assignments[u.id]?.assigned ?? false);
    return u.username.toLowerCase().contains(_search.trim().toLowerCase()) &&
        (_access == 'all' || assigned == (_access == 'assigned')) &&
        (_role == 'all' || u.role == _role) &&
        (_child == 'all' || u.child == (_child == 'yes')) &&
        (_sso == 'all' || u.ssoLinked == (_sso == 'yes')) &&
        (_invitation == 'all' || u.hasPendingInvite == (_invitation == 'yes'));
  }).toList();

  String _instanceName(String id) {
    final instances = ref.read(authProvider).valueOrNull?.connection?.instances ?? [];
    for (final instance in instances) { if (instance.id == id) return instance.name; }
    return id.isEmpty ? 'None' : 'Unavailable instance';
  }

  Future<void> _apply(bool add) async {
    final ids = _selected.where((id) => (_assignments[id]?.assigned ?? false) != add).toList()..sort();
    if (ids.isEmpty) return;
    if (!add) {
      final preferences = ids.where((id) => _assignments[id]?.preferredInstanceId == widget.instanceId).length;
      final confirmed = await showDialog<bool>(context: context, builder: (context) => AlertDialog(
        title: Text('Remove ${ids.length} users?'),
        content: Text('Remove access to ${widget.instanceName} for the selected users? Other instance assignments stay in place.'
            '${preferences > 0 ? '\n\n$preferences saved preferences will be cleared. Their default will use a remaining assigned instance, if any.' : ''}'),
        actions: [TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          TextButton(onPressed: () => Navigator.pop(context, true), child: const Text('Remove users'))],
      ));
      if (confirmed != true) return;
    }
    if (!mounted) return;
    setState(() => _saving = true);
    try {
      await _service.changeAssignments(widget.instanceId, ids, add: add);
      await ref.read(authProvider.notifier).refreshConfig();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('${add ? 'Added' : 'Removed'} ${ids.length} users')));
      await _load();
    } catch (_) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Could not update assignments. Reload and try again.')));
    } finally { if (mounted) setState(() => _saving = false); }
  }

  Widget _filter(String label, String value, Map<String, String> options, void Function(String) update) => SizedBox(
    width: 260,
    child: DropdownButtonFormField<String>(
      initialValue: value,
      isExpanded: true,
      decoration: InputDecoration(labelText: label),
      items: options.entries.map((e) => DropdownMenuItem(value: e.key, child: Text(e.value, overflow: TextOverflow.ellipsis))).toList(),
      onChanged: _saving ? null : (v) { if (v != null) setState(() { update(v); _selected.clear(); }); },
    ),
  );

  @override
  Widget build(BuildContext context) {
    final matching = _matching;
    final addCount = _selected.where((id) => !(_assignments[id]?.assigned ?? false)).length;
    final removeCount = _selected.length - addCount;
    return Scaffold(
      appBar: AppBar(title: Text('Manage users · ${widget.instanceName}')),
      body: CenteredContent(child: _loading ? const Center(child: CircularProgressIndicator()) :
        _error != null ? Center(child: Column(mainAxisSize: MainAxisSize.min, children: [Text(_error!), TextButton(onPressed: _load, child: const Text('Retry'))])) :
        ListView(padding: const EdgeInsets.all(16), children: [
          TextField(decoration: const InputDecoration(labelText: 'Search users', prefixIcon: Icon(Icons.search)),
            enabled: !_saving, onChanged: (v) => setState(() { _search = v; _selected.clear(); })),
          const SizedBox(height: 12),
          Wrap(spacing: 16, runSpacing: 12, children: [
            _filter('Access', _access, {'all': 'All users', 'assigned': 'Assigned', 'unassigned': 'Unassigned'}, (v) => _access = v),
            _filter('Role', _role, {'all': 'All roles', 'user': 'User', 'admin': 'Administrator'}, (v) => _role = v),
            _filter('Child account', _child, {'all': 'All accounts', 'yes': 'Children', 'no': 'Adults'}, (v) => _child = v),
            _filter('SSO linked', _sso, {'all': 'All accounts', 'yes': 'Linked', 'no': 'Not linked'}, (v) => _sso = v),
            _filter('Invitation', _invitation, {'all': 'All accounts', 'yes': 'Pending invitation', 'no': 'No pending invitation'}, (v) => _invitation = v),
          ]),
          const SizedBox(height: 16),
          Text('${matching.length} matching · ${_selected.length} selected'),
          Wrap(spacing: 8, children: [
            TextButton(onPressed: _saving ? null : () => setState(() { _selected.addAll(matching.where((u) => !u.isAdmin).map((u) => u.id)); }), child: const Text('Select all matching')),
            TextButton(onPressed: _saving ? null : () => setState(_selected.clear), child: const Text('Clear selection')),
            FilledButton(onPressed: _saving || addCount == 0 ? null : () => _apply(true), child: Text('Add selected ($addCount)')),
            OutlinedButton(onPressed: _saving || removeCount == 0 ? null : () => _apply(false), child: Text('Remove selected ($removeCount)')),
          ]),
          if (_saving) const LinearProgressIndicator(),
          if (matching.isEmpty) const Padding(padding: EdgeInsets.all(24), child: Text('No users match these filters.')),
          for (final user in matching) CheckboxListTile(
            key: ValueKey('assignment-${user.id}'),
            title: Text(user.username),
            subtitle: Text(user.isAdmin ? 'Administrator · Access to all instances' :
              '${_assignments[user.id]?.assigned == true ? 'Assigned' : 'Unassigned'} · Default: ${_instanceName(_assignments[user.id]?.effectiveDefaultId ?? '')}'),
            value: _selected.contains(user.id),
            onChanged: _saving || user.isAdmin ? null : (v) => setState(() { if (v == true) { _selected.add(user.id); } else { _selected.remove(user.id); } }),
          ),
        ])),
    );
  }
}
