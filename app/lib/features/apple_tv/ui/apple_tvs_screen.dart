import 'package:cantinarr/l10n/operations_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
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
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) ref.invalidate(appleTVsProvider);
  }

  Future<void> _add() async {
    await showAppSheet<void>(context,
        builder: (_) => _DiscoverySheet(
              service: ref.read(appleTVServiceProvider),
            ));
    if (mounted) ref.invalidate(appleTVsProvider);
  }

  Future<void> _edit(AppleTV tv) async {
    final service = ref.read(appleTVServiceProvider);
    await showAppSheet<void>(context,
        builder: (_) => _TVEditor(
              tv: tv,
              service: service,
              loadUsers: ref.read(authProvider.notifier).listUsers,
            ));
    if (mounted) ref.invalidate(appleTVsProvider);
  }

  @override
  Widget build(BuildContext context) {
    final auth = ref.watch(authProvider).valueOrNull;
    final allowed =
        auth?.user?.isAdmin == true && auth?.connection?.appleTvRemote == true;
    final tvs = ref.watch(appleTVsProvider);
    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.opsAppleTVs)),
      body: CenteredContent(
        child: !allowed
            ? Center(child: Text(context.l10n.opsAppleTVSetupIsNotAvailableFor))
            : tvs.when(
                skipLoadingOnReload: false,
                loading: () => const Center(child: CircularProgressIndicator()),
                error: (_, __) => Center(
                    child: TextButton.icon(
                        onPressed: () => ref.invalidate(appleTVsProvider),
                        icon: const Icon(Icons.refresh),
                        label: Text(context.l10n.opsCouldnTLoadAppleTVsRetry))),
                data: (list) =>
                    ListView(padding: const EdgeInsets.all(16), children: [
                  Text(context.l10n.opsOpenMoviesAndShowsInInfuseOn),
                  const SizedBox(height: 12),
                  Text(context.l10n.opsAdminsCanUseEveryPairedTVYou),
                  const SizedBox(height: 16),
                  if (!list.supported)
                    Text(context.l10n.opsTheAppleTVHelperIsMissingOfficial),
                  if (list.supported)
                    Align(
                        alignment: Alignment.centerLeft,
                        child: FilledButton.icon(
                            onPressed: _add,
                            icon: const Icon(Icons.add),
                            label: Text(context.l10n.opsAddAppleTV))),
                  const SizedBox(height: 12),
                  if (list.devices.isEmpty)
                    Padding(
                        padding: EdgeInsets.symmetric(vertical: 24),
                        child:
                            Text(context.l10n.opsNoAppleTVsArePairedWithThis)),
                  for (final tv in list.devices)
                    Card(
                        child: ListTile(
                      leading: const Icon(Icons.tv),
                      title: Text(tv.name),
                      subtitle: Text(tv.address),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () => _edit(tv),
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
  void dispose() {
    _address.dispose();
    super.dispose();
  }

  Future<void> _search() async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
      _found = null;
      _searchedAddress = _address.text.trim();
    });
    try {
      final found = await widget.service.discover(_searchedAddress);
      if (mounted) setState(() => _found = found);
    } catch (error) {
      if (mounted)
        setState(() =>
            _error = localizeOperationsLabel(context, appleTVError(error)));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _pair(AppleTV tv) async {
    final paired = await showAppSheet<bool>(context,
        isDismissible: false,
        enableDrag: false,
        builder: (_) => _PairingSheet(tv: tv, service: widget.service));
    if (paired == true && mounted) Navigator.pop(context);
  }

  @override
  Widget build(BuildContext context) => AppSheet(
          child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(context.l10n.opsAddAppleTV,
              style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 12),
          Text(context.l10n.opsTurnOnTheTVAndHaveInfuse),
          const SizedBox(height: 16),
          TextField(
              controller: _address,
              enabled: !_busy,
              autocorrect: false,
              decoration: InputDecoration(
                  labelText: context.l10n.opsTVAddressOptional,
                  hintText: '192.168.1.25'),
              onSubmitted: (_) => _search()),
          const SizedBox(height: 12),
          FilledButton.icon(
              onPressed: _busy ? null : _search,
              icon: const Icon(Icons.search),
              label: Text(_busy
                  ? context.l10n.opsSearching
                  : context.l10n.opsSearchForTVs)),
          if (_error != null) _ErrorText(_error!),
          if (_found?.isEmpty == true)
            Padding(
                padding: const EdgeInsets.only(top: 16),
                child: Text(_searchedAddress.isEmpty
                    ? context.l10n.opsNoAppleTVsAnsweredLocalDiscoveryA
                    : context.l10n.opsNoAppleTVAnsweredAtThatAddress)),
          for (final tv in _found ?? <AppleTV>[])
            ListTile(
                leading: const Icon(Icons.tv),
                title: Text(tv.name),
                subtitle: Text(tv.address),
                trailing: Text(context.l10n.opsPair),
                onTap: () => _pair(tv)),
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
  void initState() {
    super.initState();
    _begin();
  }

  @override
  void dispose() {
    final id = _id;
    if (id != null) unawaited(widget.service.cancel(id).catchError((_) {}));
    _pin.clear();
    _pin.dispose();
    super.dispose();
  }

  Future<void> _begin() async {
    setState(() {
      _busy = true;
      _failed = false;
      _error = null;
    });
    try {
      final id = await widget.service.begin(widget.tv);
      if (!mounted) {
        await widget.service.cancel(id);
        return;
      }
      setState(() => _id = id);
    } catch (error) {
      if (mounted)
        setState(() {
          _error = localizeOperationsLabel(context, appleTVError(error));
          _failed = true;
        });
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
      setState(() => _error = context.l10n.opsEnterTheFourDigitPINShownOn);
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    final pin = _pin.text;
    _pin.clear();
    try {
      await widget.service.complete(_id!, pin);
      _id = null;
      if (mounted) Navigator.pop(context, true);
    } catch (error) {
      if (mounted)
        setState(() {
          _error = localizeOperationsLabel(context, appleTVError(error));
          _failed = true;
        });
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => AppSheet(
          child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(context.l10n.opsPairNamedTv(widget.tv.name),
              style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 12),
          Text(_failed
              ? context.l10n.opsPairingDidNotFinishStartAgainWhen
              : _id == null
                  ? context.l10n.opsConnectingToTheTV
                  : context.l10n.opsEnterTheFourDigitPINShownOnMessage),
          const SizedBox(height: 16),
          if (_id != null && !_failed)
            TextField(
                controller: _pin,
                enabled: !_busy,
                autofocus: true,
                keyboardType: TextInputType.number,
                obscureText: true,
                enableSuggestions: false,
                autocorrect: false,
                inputFormatters: [
                  FilteringTextInputFormatter.digitsOnly,
                  LengthLimitingTextInputFormatter(4)
                ],
                decoration: InputDecoration(labelText: context.l10n.opsTVPIN),
                onSubmitted: (_) => _complete()),
          if (_error != null) _ErrorText(_error!),
          const SizedBox(height: 16),
          Wrap(spacing: 12, children: [
            if (_failed)
              FilledButton(
                  onPressed: _busy ? null : _restart,
                  child: Text(context.l10n.opsStartAgain)),
            if (_id != null && !_failed)
              FilledButton(
                  onPressed: _busy ? null : _complete,
                  child: Text(_busy
                      ? context.l10n.opsPairing
                      : context.l10n.opsPairTV)),
            TextButton(
                onPressed: () => Navigator.pop(context),
                child: Text(context.l10n.opsCancel)),
          ]),
          const SizedBox(height: 24),
        ],
      ));
}

class _TVEditor extends StatefulWidget {
  const _TVEditor(
      {required this.tv, required this.service, required this.loadUsers});
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
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _name.dispose();
    _address.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    await _run(() async {
      final users = await widget.loadUsers();
      final grants = await widget.service.grants(widget.tv.id);
      if (mounted)
        setState(() {
          _users = users;
          _grants = grants;
        });
    });
  }

  Future<void> _run(Future<void> Function() action) async {
    setState(() {
      _busy = true;
      _error = null;
      _status = null;
    });
    try {
      await action();
    } catch (error) {
      if (mounted)
        setState(() =>
            _error = localizeOperationsLabel(context, appleTVError(error)));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _save() => _run(() async {
        await widget.service.update(widget.tv.id, _name.text, _address.text);
        await widget.service.saveGrants(widget.tv.id, _grants);
        if (mounted) Navigator.pop(context);
      });
  Future<void> _check() => _run(() async {
        await widget.service.check(widget.tv.id);
        if (mounted)
          setState(() => _status = context.l10n.opsConnectedInfuseIsInstalled);
      });
  Future<void> _repair() async {
    final result = await showAppSheet<bool>(context,
        isDismissible: false,
        enableDrag: false,
        builder: (_) => _PairingSheet(
            tv: AppleTV(
                name: _name.text.trim(),
                address: _address.text.trim(),
                identifier: widget.tv.identifier),
            service: widget.service));
    if (result == true && mounted)
      setState(() => _status = context.l10n.opsPairedAgain);
  }

  Future<void> _forget() async {
    final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
              title: Text(context.l10n.opsForgetNamedTv(widget.tv.name)),
              content: Text(context.l10n.opsRemovesItsSavedPairingAndEveryoneS),
              actions: [
                TextButton(
                    onPressed: () => Navigator.pop(context, false),
                    child: Text(context.l10n.opsCancel)),
                TextButton(
                    onPressed: () => Navigator.pop(context, true),
                    child: Text(context.l10n.opsForget))
              ],
            ));
    if (confirmed != true || !mounted) return;
    await _run(() async {
      await widget.service.forget(widget.tv.id);
      if (mounted) Navigator.pop(context);
    });
  }

  @override
  Widget build(BuildContext context) => AppSheet(
          child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(widget.tv.name, style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 16),
          TextField(
              controller: _name,
              enabled: !_busy,
              decoration: InputDecoration(labelText: context.l10n.opsName),
              maxLength: 128),
          TextField(
              controller: _address,
              enabled: !_busy,
              autocorrect: false,
              decoration: InputDecoration(
                  labelText: context.l10n.opsTVAddress,
                  helperText: context
                      .l10n.opsSaveAddressChangesBeforeCheckingTheConnection)),
          const SizedBox(height: 20),
          Text(context.l10n.opsWhoCanUseThisTV,
              style: Theme.of(context).textTheme.titleMedium),
          Text(context.l10n.opsAllAdminsHaveAccessChooseOtherAdults),
          if (_users == null && !_busy)
            TextButton(
                onPressed: _load,
                child: Text(context.l10n.opsRetryLoadingPeople)),
          for (final user in _users ?? <UserSummary>[])
            if (!user.isAdmin && !user.child)
              CheckboxListTile(
                  contentPadding: EdgeInsets.zero,
                  title: Text(user.username),
                  value: _grants.contains(user.id),
                  onChanged: _busy
                      ? null
                      : (value) => setState(() {
                            if (value == true) {
                              _grants.add(user.id);
                            } else {
                              _grants.remove(user.id);
                            }
                          })),
          if (_error != null) _ErrorText(_error!),
          if (_status != null)
            Padding(
                padding: const EdgeInsets.only(top: 12), child: Text(_status!)),
          const SizedBox(height: 16),
          Wrap(spacing: 8, runSpacing: 8, children: [
            FilledButton(
                onPressed: _busy || _users == null ? null : _save,
                child: Text(context.l10n.opsSave)),
            OutlinedButton(
                onPressed: _busy ? null : _check,
                child: Text(context.l10n.opsCheckConnection)),
            TextButton(
                onPressed: _busy ? null : _repair,
                child: Text(context.l10n.opsPairAgain)),
            TextButton(
                onPressed: _busy ? null : _forget,
                child: Text(context.l10n.opsForgetTV)),
          ]),
          if (_busy)
            const Padding(
                padding: EdgeInsets.only(top: 12),
                child: LinearProgressIndicator()),
          const SizedBox(height: 24),
        ],
      ));
}

class _ErrorText extends StatelessWidget {
  const _ErrorText(this.message);
  final String message;
  @override
  Widget build(BuildContext context) => Padding(
      padding: const EdgeInsets.only(top: 12),
      child: Text(message,
          style: TextStyle(color: Theme.of(context).colorScheme.error)));
}
