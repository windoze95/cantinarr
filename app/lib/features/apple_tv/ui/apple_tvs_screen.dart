import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/apple_tv_service.dart';

class AppleTVsScreen extends ConsumerStatefulWidget {
  const AppleTVsScreen({super.key});
  @override
  ConsumerState<AppleTVsScreen> createState() => _AppleTVsScreenState();
}

class _AppleTVsScreenState extends ConsumerState<AppleTVsScreen>
    with WidgetsBindingObserver {
  @override
  void initState() { super.initState(); WidgetsBinding.instance.addObserver(this); }
  @override
  void dispose() { WidgetsBinding.instance.removeObserver(this); super.dispose(); }
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) ref.invalidate(appleTVsProvider);
  }

  Future<void> _add() async {
    await showAppSheet<void>(context, builder: (_) => _DiscoverySheet(
      service: ref.read(appleTVServiceProvider),
    ));
    if (mounted) ref.invalidate(appleTVsProvider);
  }

  Future<void> _edit(AppleTV tv) async {
    final service = ref.read(appleTVServiceProvider);
    await showAppSheet<void>(context, builder: (_) => _TVEditor(
      tv: tv, service: service,
      loadUsers: ref.read(authProvider.notifier).listUsers,
    ));
    if (mounted) ref.invalidate(appleTVsProvider);
  }

  @override
  Widget build(BuildContext context) {
    final auth = ref.watch(authProvider).valueOrNull;
    final allowed = auth?.user?.isAdmin == true && auth?.connection?.appleTvRemote == true;
    final tvs = ref.watch(appleTVsProvider);
    return Scaffold(appBar: AppBar(title: const Text('Apple TVs')),
      body: CenteredContent(child: !allowed
        ? const Center(child: Text('Apple TV setup is not available for this account and server.'))
        : tvs.when(skipLoadingOnReload: false,
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, __) => Center(child: TextButton.icon(
            onPressed: () => ref.invalidate(appleTVsProvider), icon: const Icon(Icons.refresh),
            label: const Text('Couldn’t load Apple TVs · Retry'))),
          data: (list) => ListView(padding: const EdgeInsets.all(16), children: [
            const Text('Open movies and shows in Infuse on your TV from any Cantinarr app. '
              'The server must be able to reach the TV on your home network.'),
            const SizedBox(height: 12),
            const Text('Admins can use every paired TV. You can give other adults access '
              'to individual TVs. Kids accounts cannot control TVs.'),
            const SizedBox(height: 16),
            if (!list.supported) const Text('The Apple TV helper is missing. Official Cantinarr '
              'container images include it; manual server installs need the helper installed first.'),
            if (list.supported) Align(alignment: Alignment.centerLeft,
              child: FilledButton.icon(onPressed: _add, icon: const Icon(Icons.add),
                label: const Text('Add Apple TV'))),
            const SizedBox(height: 12),
            if (list.devices.isEmpty) const Padding(padding: EdgeInsets.symmetric(vertical: 24),
              child: Text('No Apple TVs are paired with this server.')),
            for (final tv in list.devices) Card(child: ListTile(
              leading: const Icon(Icons.tv), title: Text(tv.name), subtitle: Text(tv.address),
              trailing: const Icon(Icons.chevron_right), onTap: () => _edit(tv),
            )),
          ]),
        ),
      ),
    );
  }
}

class _DiscoverySheet extends StatefulWidget {
  const _DiscoverySheet({required this.service});
  final AppleTVService service;
  @override
  State<_DiscoverySheet> createState() => _DiscoverySheetState();
}

class _DiscoverySheetState extends State<_DiscoverySheet> {
  final _address = TextEditingController();
  List<AppleTV>? _found;
  bool _busy = false;
  String? _error;
  String _searchedAddress = '';
  @override
  void dispose() { _address.dispose(); super.dispose(); }

  Future<void> _search() async {
    if (_busy) return;
    setState(() { _busy = true; _error = null; _found = null; _searchedAddress = _address.text.trim(); });
    try {
      final found = await widget.service.discover(_searchedAddress);
      if (mounted) setState(() => _found = found);
    } catch (error) {
      if (mounted) setState(() => _error = appleTVError(error));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _pair(AppleTV tv) async {
    final paired = await showAppSheet<bool>(context, isDismissible: false,
      enableDrag: false, builder: (_) => _PairingSheet(tv: tv, service: widget.service));
    if (paired == true && mounted) Navigator.pop(context);
  }

  @override
  Widget build(BuildContext context) => AppSheet(child: Column(
    mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text('Add Apple TV', style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 12),
      const Text('Turn on the TV and have Infuse installed. Search the server’s local '
        'network, or enter the TV’s IP address or hostname to narrow the search. '
        'For separate subnets, enable mDNS forwarding between the server and TV networks.'),
      const SizedBox(height: 16),
      TextField(controller: _address, enabled: !_busy, autocorrect: false,
        decoration: const InputDecoration(labelText: 'TV address (optional)',
          hintText: '192.168.1.25'), onSubmitted: (_) => _search()),
      const SizedBox(height: 12),
      FilledButton.icon(onPressed: _busy ? null : _search,
        icon: const Icon(Icons.search), label: Text(_busy ? 'Searching…' : 'Search for TVs')),
      if (_error != null) _ErrorText(_error!),
      if (_found?.isEmpty == true) Padding(padding: const EdgeInsets.only(top: 16), child: Text(
        _searchedAddress.isEmpty
          ? 'No Apple TVs answered local discovery. A container or separate network can hide '
            'discovery results; this does not mean no TVs are present. Check mDNS forwarding '
            'if the server and TV are on different subnets.'
          : 'No Apple TV answered at that address. Check that the TV is awake and '
            'mDNS discovery can reach it. Entering an address does not bypass discovery.')),
      for (final tv in _found ?? <AppleTV>[]) ListTile(leading: const Icon(Icons.tv),
        title: Text(tv.name), subtitle: Text(tv.address),
        trailing: const Text('Pair'), onTap: () => _pair(tv)),
      const SizedBox(height: 24),
    ],
  ));
}

class _PairingSheet extends StatefulWidget {
  const _PairingSheet({required this.tv, required this.service});
  final AppleTV tv;
  final AppleTVService service;
  @override
  State<_PairingSheet> createState() => _PairingSheetState();
}

class _PairingSheetState extends State<_PairingSheet> {
  final _pin = TextEditingController();
  String? _id;
  String? _error;
  bool _busy = true;
  bool _failed = false;
  @override
  void initState() { super.initState(); _begin(); }
  @override
  void dispose() {
    final id = _id;
    if (id != null) unawaited(widget.service.cancel(id).catchError((_) {}));
    _pin.clear(); _pin.dispose(); super.dispose();
  }

  Future<void> _begin() async {
    setState(() { _busy = true; _failed = false; _error = null; });
    try {
      final id = await widget.service.begin(widget.tv);
      if (!mounted) { await widget.service.cancel(id); return; }
      setState(() => _id = id);
    } catch (error) {
      if (mounted) setState(() { _error = appleTVError(error); _failed = true; });
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _restart() async {
    if (_busy) return;
    setState(() => _busy = true);
    final id = _id;
    _id = null;
    _pin.clear();
    if (id != null) await widget.service.cancel(id).catchError((_) {});
    if (mounted) await _begin();
  }

  Future<void> _complete() async {
    if (_busy || _id == null) return;
    if (_pin.text.length != 4) {
      setState(() => _error = 'Enter the four-digit PIN shown on the TV.');
      return;
    }
    setState(() { _busy = true; _error = null; });
    final pin = _pin.text;
    _pin.clear();
    try {
      await widget.service.complete(_id!, pin);
      _id = null;
      if (mounted) Navigator.pop(context, true);
    } catch (error) {
      if (mounted) setState(() { _error = appleTVError(error); _failed = true; });
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => AppSheet(child: Column(
    mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text('Pair ${widget.tv.name}', style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 12),
      Text(_failed ? 'Pairing did not finish. Start again when the TV is ready.'
        : _id == null ? 'Connecting to the TV…'
        : 'Enter the four-digit PIN shown on your TV. Pairing expires after five minutes.'),
      const SizedBox(height: 16),
      if (_id != null && !_failed) TextField(controller: _pin, enabled: !_busy,
        autofocus: true, keyboardType: TextInputType.number, obscureText: true,
        enableSuggestions: false, autocorrect: false,
        inputFormatters: [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(4)],
        decoration: const InputDecoration(labelText: 'TV PIN'),
        onSubmitted: (_) => _complete()),
      if (_error != null) _ErrorText(_error!),
      const SizedBox(height: 16),
      Wrap(spacing: 12, children: [
        if (_failed) FilledButton(onPressed: _busy ? null : _restart,
          child: const Text('Start again')),
        if (_id != null && !_failed) FilledButton(onPressed: _busy ? null : _complete,
          child: Text(_busy ? 'Pairing…' : 'Pair TV')),
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
      ]),
      const SizedBox(height: 24),
    ],
  ));
}

class _TVEditor extends StatefulWidget {
  const _TVEditor({required this.tv, required this.service, required this.loadUsers});
  final AppleTV tv;
  final AppleTVService service;
  final Future<List<UserSummary>> Function() loadUsers;
  @override
  State<_TVEditor> createState() => _TVEditorState();
}

class _TVEditorState extends State<_TVEditor> {
  late final _name = TextEditingController(text: widget.tv.name);
  late final _address = TextEditingController(text: widget.tv.address);
  List<UserSummary>? _users;
  Set<int> _grants = {};
  bool _busy = true;
  String? _error;
  String? _status;
  @override
  void initState() { super.initState(); _load(); }
  @override
  void dispose() { _name.dispose(); _address.dispose(); super.dispose(); }

  Future<void> _load() async {
    await _run(() async {
      final users = await widget.loadUsers();
      final grants = await widget.service.grants(widget.tv.id);
      if (mounted) setState(() { _users = users; _grants = grants; });
    });
  }
  Future<void> _run(Future<void> Function() action) async {
    setState(() { _busy = true; _error = null; _status = null; });
    try { await action(); }
    catch (error) { if (mounted) setState(() => _error = appleTVError(error)); }
    finally { if (mounted) setState(() => _busy = false); }
  }
  Future<void> _save() => _run(() async {
    await widget.service.update(widget.tv.id, _name.text, _address.text);
    await widget.service.saveGrants(widget.tv.id, _grants);
    if (mounted) Navigator.pop(context);
  });
  Future<void> _check() => _run(() async {
    await widget.service.check(widget.tv.id);
    if (mounted) setState(() => _status = 'Connected. Infuse is installed.');
  });
  Future<void> _repair() async {
    final result = await showAppSheet<bool>(context, isDismissible: false, enableDrag: false,
      builder: (_) => _PairingSheet(tv: AppleTV(name: _name.text.trim(),
        address: _address.text.trim(), identifier: widget.tv.identifier), service: widget.service));
    if (result == true && mounted) setState(() => _status = 'Paired again.');
  }
  Future<void> _forget() async {
    final confirmed = await showDialog<bool>(context: context, builder: (context) => AlertDialog(
      title: Text('Forget ${widget.tv.name}?'),
      content: const Text('Removes its saved pairing and everyone’s access in Cantinarr. '
        'Infuse and its libraries stay on the TV.'),
      actions: [TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
        TextButton(onPressed: () => Navigator.pop(context, true), child: const Text('Forget'))],
    ));
    if (confirmed != true || !mounted) return;
    await _run(() async { await widget.service.forget(widget.tv.id);
      if (mounted) Navigator.pop(context); });
  }

  @override
  Widget build(BuildContext context) => AppSheet(child: Column(
    mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(widget.tv.name, style: Theme.of(context).textTheme.titleLarge),
      const SizedBox(height: 16),
      TextField(controller: _name, enabled: !_busy,
        decoration: const InputDecoration(labelText: 'Name'), maxLength: 128),
      TextField(controller: _address, enabled: !_busy, autocorrect: false,
        decoration: const InputDecoration(labelText: 'TV address',
          helperText: 'Save address changes before checking the connection.')),
      const SizedBox(height: 20),
      Text('Who can use this TV', style: Theme.of(context).textTheme.titleMedium),
      const Text('All admins have access. Choose other adults below.'),
      if (_users == null && !_busy) TextButton(onPressed: _load, child: const Text('Retry loading people')),
      for (final user in _users ?? <UserSummary>[])
        if (!user.isAdmin && !user.child) CheckboxListTile(contentPadding: EdgeInsets.zero,
          title: Text(user.username), value: _grants.contains(user.id),
          onChanged: _busy ? null : (value) => setState(() {
            if (value == true) { _grants.add(user.id); } else { _grants.remove(user.id); }
          })),
      if (_error != null) _ErrorText(_error!),
      if (_status != null) Padding(padding: const EdgeInsets.only(top: 12), child: Text(_status!)),
      const SizedBox(height: 16),
      Wrap(spacing: 8, runSpacing: 8, children: [
        FilledButton(onPressed: _busy || _users == null ? null : _save, child: const Text('Save')),
        OutlinedButton(onPressed: _busy ? null : _check, child: const Text('Check connection')),
        TextButton(onPressed: _busy ? null : _repair, child: const Text('Pair again')),
        TextButton(onPressed: _busy ? null : _forget, child: const Text('Forget TV')),
      ]),
      if (_busy) const Padding(padding: EdgeInsets.only(top: 12), child: LinearProgressIndicator()),
      const SizedBox(height: 24),
    ],
  ));
}

class _ErrorText extends StatelessWidget {
  const _ErrorText(this.message);
  final String message;
  @override
  Widget build(BuildContext context) => Padding(padding: const EdgeInsets.only(top: 12),
    child: Text(message, style: TextStyle(color: Theme.of(context).colorScheme.error)));
}
