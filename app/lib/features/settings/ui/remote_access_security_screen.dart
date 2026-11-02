import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/api_error_message.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../data/remote_access_security_service.dart';

class RemoteAccessSecurityScreen extends ConsumerStatefulWidget {
  const RemoteAccessSecurityScreen({super.key});

  @override
  ConsumerState<RemoteAccessSecurityScreen> createState() =>
      _RemoteAccessSecurityScreenState();
}

class _RemoteAccessSecurityScreenState
    extends ConsumerState<RemoteAccessSecurityScreen> {
  final _draft = SettingsDraft();
  final _trustedProxies = TextEditingController();
  RemoteAccessSecuritySettings? _settings;
  bool _requireHTTPS = false;
  bool _trustCGNAT = false;
  bool _busy = false;
  String? _error;

  Object get _draftValues => [
        _requireHTTPS,
        _trustedProxies.text,
        _trustCGNAT,
      ];

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _trustedProxies.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final settings =
          await ref.read(remoteAccessSecurityServiceProvider).fetch();
      if (!mounted) return;
      _apply(settings);
    } catch (e) {
      if (mounted) setState(() => _error = apiErrorMessage(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _apply(RemoteAccessSecuritySettings settings) {
    _trustedProxies.text = settings.trustedProxyCIDRs.join('\n');
    setState(() {
      _settings = settings;
      _requireHTTPS = settings.requireHTTPSOutsideLocalNetwork;
      _trustCGNAT = settings.trustCGNATAsLocal;
      _draft.markSaved(_draftValues);
      _error = null;
    });
  }

  List<String> _proxyValues() => _trustedProxies.text
      .split(RegExp(r'[,\s]+'))
      .map((value) => value.trim())
      .where((value) => value.isNotEmpty)
      .toList();

  Future<void> _save() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final settings =
          await ref.read(remoteAccessSecurityServiceProvider).update(
                requireHTTPSOutsideLocalNetwork: _requireHTTPS,
                trustedProxyCIDRs: _proxyValues(),
                trustCGNATAsLocal: _trustCGNAT,
              );
      if (!mounted) return;
      _apply(settings);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(context.l10n.settingsSaved)),
      );
    } catch (e) {
      if (mounted) setState(() => _error = apiErrorMessage(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  String _connectionLabel(String value) => switch (value) {
        'https' => context.l10n.settingsConnectionHTTPS,
        'local_http' => context.l10n.settingsConnectionLocalHTTP,
        'remote_http' => context.l10n.settingsConnectionRemoteHTTP,
        'untrusted_proxy' => context.l10n.settingsConnectionUntrustedProxy,
        _ => context.l10n.settingsConnectionUnknownHTTP,
      };

  Widget _managedNote(bool managed) => managed
      ? Padding(
          padding: const EdgeInsets.only(top: 4),
          child: Text(
            context.l10n.settingsManagedByEnvironment,
            style: Theme.of(context).textTheme.bodySmall,
          ),
        )
      : const SizedBox.shrink();

  @override
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () => _draft.hasChanges(_draftValues),
        isSaving: _busy,
        child: Scaffold(
          appBar: AppBar(title: Text(context.l10n.settingsRemoteAccessSecurity)),
          body: Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 700),
              child: ListView(
                padding: const EdgeInsets.all(24),
                children: [
                  if (_busy) const LinearProgressIndicator(),
                  if (_error != null) ...[
                    Text(
                      _error!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.error,
                      ),
                    ),
                    Align(
                      alignment: Alignment.centerLeft,
                      child: TextButton(
                        onPressed: _busy ? null : _load,
                        child: Text(context.l10n.settingsReloadSettings),
                      ),
                    ),
                  ],
                  if (_settings case final settings?) ...[
                    Text(context.l10n.settingsRemoteAccessSecurityDescription),
                    const SizedBox(height: 16),
                    ListTile(
                      contentPadding: EdgeInsets.zero,
                      leading: const Icon(Icons.lan_outlined),
                      title: Text(context.l10n.settingsCurrentConnection),
                      subtitle: Text(
                        _connectionLabel(settings.connectionSecurity),
                      ),
                    ),
                    SwitchListTile(
                      contentPadding: EdgeInsets.zero,
                      title: Text(
                        context.l10n
                            .settingsRequireHTTPSOutsideLocalNetwork,
                      ),
                      subtitle: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(context.l10n
                              .settingsRequireHTTPSOutsideLocalNetworkHelp),
                          _managedNote(settings.requireHTTPSManaged),
                        ],
                      ),
                      value: _requireHTTPS,
                      onChanged: _busy || settings.requireHTTPSManaged
                          ? null
                          : (value) => setState(() => _requireHTTPS = value),
                    ),
                    const SizedBox(height: 20),
                    TextField(
                      controller: _trustedProxies,
                      enabled: !_busy && !settings.trustedProxyCIDRsManaged,
                      minLines: 2,
                      maxLines: 6,
                      autocorrect: false,
                      decoration: InputDecoration(
                        labelText: context.l10n.settingsTrustedProxyNetworks,
                        helperText:
                            context.l10n.settingsTrustedProxyNetworksHelp,
                        helperMaxLines: 4,
                        hintText: '192.168.1.10\n172.18.0.0/16',
                      ),
                    ),
                    _managedNote(settings.trustedProxyCIDRsManaged),
                    const SizedBox(height: 12),
                    SwitchListTile(
                      contentPadding: EdgeInsets.zero,
                      title: Text(context.l10n.settingsTrustCGNATAsLocal),
                      subtitle: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(context.l10n.settingsTrustCGNATAsLocalHelp),
                          _managedNote(settings.trustCGNATManaged),
                        ],
                      ),
                      value: _trustCGNAT,
                      onChanged: _busy || settings.trustCGNATManaged
                          ? null
                          : (value) => setState(() => _trustCGNAT = value),
                    ),
                    const SizedBox(height: 24),
                    FilledButton.icon(
                      onPressed: _busy ? null : _save,
                      icon: const Icon(Icons.save_outlined),
                      label: Text(context.l10n.settingsSave),
                    ),
                  ],
                ],
              ),
            ),
          ),
        ),
      );
}
