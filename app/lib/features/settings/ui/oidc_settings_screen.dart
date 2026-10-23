import 'package:cantinarr/l10n/l10n.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../auth/logic/auth_provider.dart';

String oidcError(Object error, {BuildContext? context}) {
  if (error is DioException && error.response?.data is Map) {
    return (error.response!.data as Map)['error']?.toString() ??
        (context?.l10n ?? lookupAppLocalizations(const Locale('en')))
            .settingsSingleSignOnCouldNotBeCompleted;
  }
  return error.toString().replaceFirst('Bad state: ', '');
}

class OIDCSettingsScreen extends ConsumerStatefulWidget {
  const OIDCSettingsScreen({super.key});
  @override
  ConsumerState<OIDCSettingsScreen> createState() => _OIDCSettingsScreenState();
}

class _OIDCSettingsScreenState extends ConsumerState<OIDCSettingsScreen> {
  final _draft = SettingsDraft();
  Object get _draftValues => [
        for (final key in ['enabled', 'auto_create', 'use_proxy', 'sso_only'])
          _config?[key],
        for (final field in _fields.values) field.text,
        _config?['client_secret'],
      ];
  Map<String, dynamic>? _config;
  String? _error;
  bool _busy = false;
  final _fields = {
    for (final key in [
      'label',
      'issuer',
      'client_id',
      'client_secret',
      'additional_scopes',
      'allowed_groups',
      'group_claim'
    ])
      key: TextEditingController()
  };
  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    for (final field in _fields.values) {
      field.dispose();
    }
    super.dispose();
  }

  Future<Map<String, dynamic>> _request(
      {String method = 'GET', String suffix = '', Map<String, dynamic>? data}) {
    final connection = ref.read(authProvider).valueOrNull!.connection!;
    return ref.read(authServiceProvider).oidcRequest(
        connection.serverUrl, '/api/admin/oidc$suffix',
        method: method, accessToken: connection.accessToken, data: data);
  }

  Future<void> _load() async {
    try {
      await ref.read(authProvider.future);
      if (!mounted) return;
      final config = await _request();
      if (!mounted) return;
      for (final key in _fields.keys) {
        final value = config[key];
        _fields[key]!.text = value is List
            ? value.join(key == 'allowed_groups' ? '\n' : ' ')
            : value?.toString() ?? '';
      }
      setState(() {
        _config = config;
        _draft.markSaved(_draftValues);
        _error = null;
      });
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e, context: context));
    }
  }

  Future<bool> _save() async {
    final config = Map<String, dynamic>.from(_config!);
    for (final key in ['label', 'issuer', 'client_id', 'group_claim']) {
      config[key] = _fields[key]!.text.trim();
    }
    config['additional_scopes'] = _fields['additional_scopes']!
        .text
        .split(RegExp(r'\s+'))
        .where((s) => s.isNotEmpty)
        .toList();
    config['allowed_groups'] = _fields['allowed_groups']!
        .text
        .split('\n')
        .where((s) => s.isNotEmpty)
        .toList();
    if (_fields['client_secret']!.text.isNotEmpty) {
      config['client_secret'] = _fields['client_secret']!.text;
    }
    try {
      final saved = await _request(method: 'PUT', data: config);
      if (!mounted) return false;
      _fields['client_secret']!.clear();
      setState(() {
        _config = saved;
        _draft.markSaved(_draftValues);
      });
      return true;
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e, context: context));
      return false;
    }
  }

  Future<void> _run(String action) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      if (!await _save() || !mounted) return;
      if (action == 'test') {
        final conn = ref.read(authProvider).valueOrNull!.connection!;
        final callback = Uri.parse(_config!['callback_url'] as String);
        await ref.read(authProvider.notifier).startSSO(conn.serverUrl,
            purpose: 'test', externalOrigin: callback.origin);
      } else {
        var message = context.l10n.settingsSingleSignOnSettingsSaved;
        if (action == 'validate') {
          final result = await _request(method: 'POST', suffix: '/validate');
          message = result['message'] as String;
        }
        if (mounted) {
          ScaffoldMessenger.of(context)
              .showSnackBar(SnackBar(content: Text(message)));
        }
      }
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e, context: context));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Widget _field(String key, String label,
          {String? helper, bool secret = false, int lines = 1}) =>
      Padding(
        padding: const EdgeInsets.only(bottom: 18),
        child: TextField(
          controller: _fields[key],
          obscureText: secret,
          maxLines: lines,
          autocorrect: false,
          enableSuggestions: !secret,
          enabled: !_busy,
          decoration: InputDecoration(
              labelText: label, helperText: helper, helperMaxLines: 4),
        ),
      );
  Widget _switch(String key, String title, String subtitle) => SwitchListTile(
        contentPadding: EdgeInsets.zero,
        title: Text(title),
        subtitle: Text(subtitle),
        value: _config![key] == true,
        onChanged:
            _busy ? null : (value) => setState(() => _config![key] = value),
      );
  @override
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () => _draft.hasChanges(_draftValues),
        isSaving: _busy,
        child: _buildPage(context),
      );

  Widget _buildPage(BuildContext context) => Scaffold(
        appBar: AppBar(title: Text(context.l10n.settingsSingleSignOn)),
        body: Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 680),
              child: ListView(padding: const EdgeInsets.all(24), children: [
                if (_error != null) ...[
                  Text(_error!,
                      style: TextStyle(
                          color: Theme.of(context).colorScheme.error)),
                  TextButton(
                      onPressed: _load,
                      child: Text(context.l10n.settingsReloadSettings))
                ],
                if (_config == null && _error == null)
                  const Center(child: CircularProgressIndicator()),
                if (_config != null) ...[
                  Text(
                      context.l10n.settingsUseOneOpenidConnectProviderToSignIn),
                  _switch('enabled', context.l10n.settingsEnableSingleSignOn,
                      context.l10n.settingsShowTheProviderOnTheSignInScreen),
                  _field('label', context.l10n.settingsProviderLabel),
                  _field('issuer', context.l10n.settingsIssuerUrl,
                      helper: context.l10n
                          .settingsTheProviderSHttpsIssuerAddressIncludingIts),
                  _field('client_id', context.l10n.settingsClientId),
                  _field('client_secret', context.l10n.settingsClientSecret,
                      secret: true,
                      helper: _config!['has_secret'] == true
                          ? context.l10n.settingsASecretIsSavedLeaveBlankToKeep
                          : context.l10n
                              .settingsOptionalForAPublicClientStoredEncryptedOn),
                  if (_config!['has_secret'] == true)
                    TextButton(
                        onPressed: _busy
                            ? null
                            : () async {
                                setState(() => _config!['client_secret'] = '');
                                await _run('save');
                              },
                        child:
                            Text(context.l10n.settingsRemoveSavedClientSecret)),
                  Text(context.l10n.settingsRegisteredCallback,
                      style: TextStyle(fontWeight: FontWeight.bold)),
                  const SizedBox(height: 8),
                  SelectableText((_config!['callback_url'] as String?)
                              ?.isNotEmpty ==
                          true
                      ? _config!['callback_url'] as String
                      : context.l10n
                          .settingsSetAnHttpsExternalAddressInSettingsFirst),
                  TextButton.icon(
                      onPressed: () => Clipboard.setData(ClipboardData(
                          text: _config!['callback_url'] as String? ?? '')),
                      icon: const Icon(Icons.copy),
                      label: Text(context.l10n.settingsCopyCallback)),
                  const SizedBox(height: 12),
                  _field('additional_scopes',
                      context.l10n.settingsAdditionalScopes,
                      helper: context.l10n
                          .settingsOpenidProfileEmailAreAlwaysRequestedSeparateAdditional),
                  _field('group_claim', context.l10n.settingsGroupClaim),
                  _field('allowed_groups', context.l10n.settingsAllowedGroups,
                      lines: 3,
                      helper: context
                          .l10n.settingsOptionalOneExactGroupNamePerLineA),
                  _switch(
                      'auto_create',
                      context.l10n.settingsCreateAccountsAutomatically,
                      context.l10n
                          .settingsOffByDefaultNewIdentitiesBecomeOrdinaryUsers),
                  _switch(
                      'use_proxy',
                      context.l10n.settingsUseOutboundProxy,
                      context
                          .l10n.settingsUseTheServerSOutboundProxyForDiscovery),
                  _switch(
                      'sso_only',
                      context.l10n.settingsRequireSingleSignOn,
                      context.l10n
                          .settingsSavingThisSignsOutRegularUsersWithLocal),
                  Text(context
                      .l10n.settingsBeforeRequiringSsoCompleteTestSignInWith),
                  const SizedBox(height: 12),
                  Text(_config!['tested'] == true
                      ? context
                          .l10n.settingsTheSavedConfigurationPassedTestSignIn
                      : context.l10n
                          .settingsTheSavedConfigurationHasNotPassedTestSign),
                  const SizedBox(height: 20),
                  Wrap(spacing: 12, runSpacing: 12, children: [
                    FilledButton(
                        onPressed: _busy ? null : () => _run('save'),
                        child: Text(context.l10n.settingsSaveSettings)),
                    OutlinedButton(
                        onPressed: _busy ? null : () => _run('validate'),
                        child: Text(context.l10n.settingsValidateDiscovery)),
                    OutlinedButton(
                        onPressed: _busy ? null : () => _run('test'),
                        child: Text(context.l10n.settingsTestSignIn)),
                  ]),
                  const SizedBox(height: 12),
                  Text(context
                      .l10n.settingsTestSignInSavesTheseSettingsAndOpens),
                ],
              ]),
            )),
      );
}
