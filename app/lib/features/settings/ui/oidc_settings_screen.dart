import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../auth/logic/auth_provider.dart';

String oidcError(Object error) {
  if (error is DioException && error.response?.data is Map) {
    return (error.response!.data as Map)['error']?.toString() ??
        'Single sign-on could not be completed.';
  }
  return error.toString().replaceFirst('Bad state: ', '');
}

class OIDCSettingsScreen extends ConsumerStatefulWidget {
  const OIDCSettingsScreen({super.key});
  @override
  ConsumerState<OIDCSettingsScreen> createState() => _OIDCSettingsScreenState();
}

class _OIDCSettingsScreenState extends ConsumerState<OIDCSettingsScreen> {
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
        _error = null;
      });
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
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
      setState(() => _config = saved);
      return true;
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
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
        var message = 'Single sign-on settings saved.';
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
      if (mounted) setState(() => _error = oidcError(e));
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
  Widget build(BuildContext context) => Scaffold(
        appBar: AppBar(title: const Text('Single sign-on')),
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
                      onPressed: _load, child: const Text('Reload settings'))
                ],
                if (_config == null && _error == null)
                  const Center(child: CircularProgressIndicator()),
                if (_config != null) ...[
                  const Text(
                      'Use one OpenID Connect provider to sign in. Cantinarr continues to manage accounts, roles and access.'),
                  _switch('enabled', 'Enable single sign-on',
                      'Show the provider on the sign-in screen.'),
                  _field('label', 'Provider label'),
                  _field('issuer', 'Issuer URL',
                      helper:
                          'The provider’s HTTPS issuer address, including its realm or application path.'),
                  _field('client_id', 'Client ID'),
                  _field('client_secret', 'Client secret',
                      secret: true,
                      helper: _config!['has_secret'] == true
                          ? 'A secret is saved. Leave blank to keep it.'
                          : 'Optional for a public client. Stored encrypted on the server.'),
                  if (_config!['has_secret'] == true)
                    TextButton(
                        onPressed: _busy
                            ? null
                            : () async {
                                setState(() => _config!['client_secret'] = '');
                                await _run('save');
                              },
                        child: const Text('Remove saved client secret')),
                  const Text('Registered callback',
                      style: TextStyle(fontWeight: FontWeight.bold)),
                  const SizedBox(height: 8),
                  SelectableText(
                      (_config!['callback_url'] as String?)?.isNotEmpty == true
                          ? _config!['callback_url'] as String
                          : 'Set an HTTPS External Address in Settings first.'),
                  TextButton.icon(
                      onPressed: () => Clipboard.setData(ClipboardData(
                          text: _config!['callback_url'] as String? ?? '')),
                      icon: const Icon(Icons.copy),
                      label: const Text('Copy callback')),
                  const SizedBox(height: 12),
                  _field('additional_scopes', 'Additional scopes',
                      helper:
                          'openid profile email are always requested. Separate additional scopes with spaces.'),
                  _field('group_claim', 'Group claim'),
                  _field('allowed_groups', 'Allowed groups',
                      lines: 3,
                      helper:
                          'Optional. One exact group name per line. A user must belong to at least one; missing or malformed groups deny sign-in.'),
                  _switch('auto_create', 'Create accounts automatically',
                      'Off by default. New identities become ordinary users. Existing accounts must be explicitly linked.'),
                  _switch('use_proxy', 'Use outbound proxy',
                      'Use the server’s outbound proxy for discovery, keys, token exchange and UserInfo. Direct connections are the default.'),
                  _switch('sso_only', 'Require single sign-on',
                      'Saving this signs out regular users with local sessions. Invitations also require SSO. Administrators keep local recovery access.'),
                  const Text(
                      'Before requiring SSO, complete Test sign-in with the saved configuration and give at least one administrator a local password. Turn off this requirement before changing provider settings.'),
                  const SizedBox(height: 12),
                  Text(_config!['tested'] == true
                      ? 'The saved configuration passed Test sign-in.'
                      : 'The saved configuration has not passed Test sign-in.'),
                  const SizedBox(height: 20),
                  Wrap(spacing: 12, runSpacing: 12, children: [
                    FilledButton(
                        onPressed: _busy ? null : () => _run('save'),
                        child: const Text('Save settings')),
                    OutlinedButton(
                        onPressed: _busy ? null : () => _run('validate'),
                        child: const Text('Validate discovery')),
                    OutlinedButton(
                        onPressed: _busy ? null : () => _run('test'),
                        child: const Text('Test sign-in')),
                  ]),
                  const SizedBox(height: 12),
                  const Text(
                      'Test sign-in saves these settings and opens your browser. It never creates or links an account. If you close the browser, your current session stays signed in.'),
                ],
              ]),
            )),
      );
}
