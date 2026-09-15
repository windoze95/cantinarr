import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../auth/logic/auth_provider.dart';
import 'oidc_settings_screen.dart';

class PlexAuthSettingsScreen extends ConsumerStatefulWidget {
  const PlexAuthSettingsScreen({super.key});
  @override
  ConsumerState<PlexAuthSettingsScreen> createState() =>
      _PlexAuthSettingsScreenState();
}

class _PlexAuthSettingsScreenState
    extends ConsumerState<PlexAuthSettingsScreen> {
  final _draft = SettingsDraft();
  Object get _draftValues => [_config?['enabled'], _config?['auto_create']];
  Map<String, dynamic>? _config;
  List<Map<String, dynamic>>? _candidates;
  final _selected = <int>{};
  String? _error;
  bool _busy = false;
  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<Map<String, dynamic>> _request(
      {String method = 'GET', String suffix = '', Map<String, dynamic>? data}) {
    final c = ref.read(authProvider).valueOrNull!.connection!;
    return ref.read(authServiceProvider).externalSignInRequest(
        c.serverUrl, '/api/admin/plex-auth$suffix',
        method: method, accessToken: c.accessToken, data: data);
  }

  Future<void> _load() async {
    try {
      await ref.read(authProvider.future);
      final config = await _request();
      if (!mounted) return;
      setState(() {
        _config = config;
        _draft.markSaved(_draftValues);
        _error = null;
      });
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
    }
  }

  Future<void> _save() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final config = await _request(method: 'PUT', data: _config);
      if (mounted) {
        setState(() {
          _config = config;
          _draft.markSaved(_draftValues);
        });
        ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Plex sign-in settings saved.')));
      }
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _review() async {
    setState(() {
      _busy = true;
      _error = null;
      _selected.clear();
      _candidates = null;
    });
    try {
      final result = await _request(suffix: '/candidates');
      if (mounted) {
        setState(() => _candidates =
            (result['candidates'] as List).cast<Map<String, dynamic>>());
      }
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _confirm() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await _request(method: 'POST', suffix: '/confirm', data: {
        'mappings': [
          for (final c in _candidates!)
            if (_selected.contains(c['user_id']))
              {'user_id': c['user_id'], 'plex_account_id': c['plex_account_id']}
        ],
      });
      if (!mounted) return;
      await _review();
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () =>
            _draft.hasChanges(_draftValues) || _selected.isNotEmpty,
        isSaving: _busy,
        child: _buildPage(context),
      );

  Widget _buildPage(BuildContext context) => Scaffold(
        appBar: AppBar(title: const Text('Plex sign-in')),
        body: Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 720),
                child: ListView(padding: const EdgeInsets.all(24), children: [
                  const Text(
                      'Let users sign in with their linked Plex account. Removing a library share later does not remove their Cantinarr sign-in.'),
                  if (_error != null)
                    Padding(
                        padding: const EdgeInsets.symmetric(vertical: 16),
                        child: Text(_error!,
                            style: TextStyle(
                                color: Theme.of(context).colorScheme.error))),
                  if (_config == null && _error == null)
                    const Center(child: CircularProgressIndicator()),
                  if (_config != null) ...[
                    SwitchListTile(
                        title: const Text('Enable Plex sign-in'),
                        subtitle: const Text(
                            'Single sign-on policy still applies to regular users.'),
                        value: _config!['enabled'] == true,
                        onChanged: _busy
                            ? null
                            : (v) => setState(() => _config!['enabled'] = v)),
                    SwitchListTile(
                        title: const Text('Allow automatic signup'),
                        subtitle: const Text(
                            'New users must own or have accepted access to a configured Plex server. Pending invitations and friends without a share do not qualify.'),
                        value: _config!['auto_create'] == true,
                        onChanged: _busy
                            ? null
                            : (v) =>
                                setState(() => _config!['auto_create'] = v)),
                    FilledButton(
                        onPressed: _busy ? null : _save,
                        child: const Text('Save settings')),
                    const SizedBox(height: 24),
                    const Text('Review existing users',
                        style: TextStyle(
                            fontSize: 20, fontWeight: FontWeight.bold)),
                    const SizedBox(height: 8),
                    const Text(
                        'Library access and sign-in are separate. Select only accounts you recognize; confirmation checks Plex again. Unavailable or ambiguous matches cannot be confirmed. If no unique account matches, check that the recipient accepted the library invitation in their own Plex account, then load fresh accounts again.'),
                    OutlinedButton(
                        onPressed: _busy ? null : _review,
                        child: const Text('Load fresh Plex accounts')),
                    if (_busy) const LinearProgressIndicator(),
                    if (_candidates?.isEmpty == true)
                      const Text(
                          'No existing Plex media-account links were found to review. Typed email addresses are not login identities.'),
                    for (final c in _candidates ?? <Map<String, dynamic>>[])
                      CheckboxListTile(
                        value: c['confirmed'] == true ||
                            _selected.contains(c['user_id']),
                        onChanged: _busy ||
                                c['confirmed'] == true ||
                                c['reason'] != null ||
                                c['plex_account_id'] == 0
                            ? null
                            : (v) => setState(() {
                                  if (v == true) {
                                    _selected.add(c['user_id'] as int);
                                  } else {
                                    _selected.remove(c['user_id']);
                                  }
                                }),
                        title: Text(
                            '${c['username']} → ${c['plex_username'] == '' ? 'Unconfirmed Plex account' : c['plex_username']}',
                            style: TextStyle(
                                color:
                                    Theme.of(context).colorScheme.onSurface)),
                        subtitle: Text(
                            [
                              if (c['email'] != '')
                                '${c['email']} (Plex ID ${c['plex_account_id']})',
                              (c['servers'] as List)
                                  .map((s) => s['name'])
                                  .join(', '),
                              if (c['confirmed'] == true) 'Confirmed',
                              if (c['reason'] != null) c['reason'] as String,
                            ].join('\n'),
                            style: TextStyle(
                                color: Theme.of(context)
                                    .colorScheme
                                    .onSurfaceVariant)),
                        isThreeLine: true,
                      ),
                    if (_candidates != null)
                      FilledButton(
                          onPressed:
                              _busy || _selected.isEmpty ? null : _confirm,
                          child:
                              Text('Confirm selected (${_selected.length})')),
                  ],
                ]))),
      );
}
