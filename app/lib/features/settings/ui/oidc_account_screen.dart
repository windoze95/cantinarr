import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../auth/data/server_status.dart';
import '../../auth/logic/auth_provider.dart';
import 'oidc_settings_screen.dart';

class OIDCAccountScreen extends ConsumerStatefulWidget {
  final int? userId;
  const OIDCAccountScreen({super.key, this.userId});
  @override
  ConsumerState<OIDCAccountScreen> createState() => _OIDCAccountScreenState();
}

class _OIDCAccountScreenState extends ConsumerState<OIDCAccountScreen> {
  List<Map<String, dynamic>>? _identities;
  ServerStatus? _status;
  String? _error;
  bool _busy = false;
  String get _path => widget.userId == null
      ? '/api/auth/oidc/identities'
      : '/api/admin/users/${widget.userId}/oidc';
  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      await ref.read(authProvider.future);
      if (!mounted) return;
      final conn = ref.read(authProvider).valueOrNull!.connection!;
      final service = ref.read(authServiceProvider);
      final results = await Future.wait<Object>([
        service.oidcRequest(conn.serverUrl, _path,
            accessToken: conn.accessToken),
        service.getServerStatus(conn.serverUrl),
      ]);
      if (!mounted) return;
      setState(() {
        _identities =
            ((results[0] as Map<String, dynamic>)['identities'] as List)
                .cast<Map<String, dynamic>>();
        _status = results[1] as ServerStatus;
        _error = null;
      });
    } catch (e) {
      if (mounted) {
        setState(() => _error =
            e is DioException && e.response?.statusCode == 404
                ? 'This server does not support single sign-on.'
                : oidcError(e));
      }
    }
  }

  Future<void> _link() async {
    setState(() => _busy = true);
    try {
      final conn = ref.read(authProvider).valueOrNull!.connection!;
      await ref
          .read(authProvider.notifier)
          .startSSO(conn.serverUrl, purpose: 'link');
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _unlink(String issuer) async {
    final approved = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
              title: const Text('Unlink single sign-on?'),
              content: const Text(
                  'This signs out this account’s SSO devices and MCP clients. A permitted local sign-in method is required to unlink your own account.'),
              actions: [
                TextButton(
                    onPressed: () => Navigator.pop(context, false),
                    child: const Text('Cancel')),
                FilledButton(
                    onPressed: () => Navigator.pop(context, true),
                    child: const Text('Unlink'))
              ],
            ));
    if (approved != true || !mounted) return;
    setState(() => _busy = true);
    try {
      final conn = ref.read(authProvider).valueOrNull!.connection!;
      await ref.read(authServiceProvider).oidcRequest(conn.serverUrl, _path,
          method: 'DELETE',
          accessToken: conn.accessToken,
          data: {'issuer': issuer});
      await _load();
      await ref.read(authProvider.notifier).refreshUser();
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
        appBar: AppBar(title: const Text('Linked sign-in')),
        body: Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 680),
              child: ListView(padding: const EdgeInsets.all(24), children: [
                const Text(
                    'Sign in to Cantinarr and the provider to link an identity. Administrators can also confirm a Plex mapping after checking current account data. Names and emails alone never authorize sign-in.'),
                const SizedBox(height: 16),
                if (_error != null)
                  Text(_error!,
                      style: TextStyle(
                          color: Theme.of(context).colorScheme.error)),
                if (_identities == null && _error == null)
                  const Center(child: CircularProgressIndicator()),
                if (_identities?.isEmpty == true)
                  const Text(
                      'No single sign-on identity is linked to this account.'),
                for (final identity in _identities ?? <Map<String, dynamic>>[])
                  Card(
                      child: Padding(
                    padding: const EdgeInsets.all(16),
                    child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          const Text('Linked',
                              style: TextStyle(fontWeight: FontWeight.bold)),
                          SelectableText(identity['issuer'] as String),
                          const SizedBox(height: 8),
                          SelectableText('Identity: ${identity['subject']}'),
                          TextButton(
                              onPressed: _busy
                                  ? null
                                  : () => _unlink(identity['issuer'] as String),
                              child: const Text('Unlink')),
                        ]),
                  )),
                if (widget.userId == null && _status?.ssoAvailable == true) ...[
                  const SizedBox(height: 16),
                  FilledButton.icon(
                      onPressed: _busy ? null : _link,
                      icon: const Icon(Icons.link),
                      label: Text('Link with ${_status!.ssoProvider}')),
                  const SizedBox(height: 12),
                  const Text(
                      'Your browser will open. Closing it leaves your current session unchanged.'),
                ],
                _PlexIdentities(userId: widget.userId),
                if (_status?.ssoOnly == true)
                  const Padding(
                      padding: EdgeInsets.only(top: 16),
                      child: Text(
                          'This server requires single sign-on for regular users. Administrators retain local recovery access.')),
              ]),
            )),
      );
}

class _PlexIdentities extends ConsumerStatefulWidget {
  final int? userId;
  const _PlexIdentities({this.userId});
  @override
  ConsumerState<_PlexIdentities> createState() => _PlexIdentitiesState();
}

class _PlexIdentitiesState extends ConsumerState<_PlexIdentities> {
  List<Map<String, dynamic>>? _identities;
  bool _busy = false, _available = false;
  String? _error;
  String get _path => widget.userId == null
      ? '/api/auth/plex/identities'
      : '/api/admin/users/${widget.userId}/plex';
  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      await ref.read(authProvider.future);
      final c = ref.read(authProvider).valueOrNull!.connection!;
      final auth = ref.read(authServiceProvider);
      final status = await auth.getServerStatus(c.serverUrl);
      final result = await auth.externalSignInRequest(c.serverUrl, _path,
          accessToken: c.accessToken);
      if (mounted) {
        setState(() {
          _identities =
              (result['identities'] as List).cast<Map<String, dynamic>>();
          _available = status.plexAvailable;
          _error = null;
        });
      }
    } on DioException catch (e) {
      if (mounted) {
        setState(() => _error = e.response?.statusCode == 404
            ? 'This server does not support Plex sign-in.'
            : oidcError(e));
      }
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
    }
  }

  Future<void> _unlink() async {
    final confirmed = await showDialog<bool>(
        context: context,
        builder: (c) => AlertDialog(
                title: const Text('Unlink Plex sign-in?'),
                content: const Text(
                    'This signs out Plex devices and MCP clients. Library access stays as it is. Your own account needs another permitted sign-in method.'),
                actions: [
                  TextButton(
                      onPressed: () => Navigator.pop(c, false),
                      child: const Text('Cancel')),
                  FilledButton(
                      onPressed: () => Navigator.pop(c, true),
                      child: const Text('Unlink Plex'))
                ]));
    if (confirmed != true || !mounted) return;
    setState(() => _busy = true);
    try {
      final c = ref.read(authProvider).valueOrNull!.connection!;
      await ref.read(authServiceProvider).externalSignInRequest(
          c.serverUrl, _path,
          method: 'DELETE', accessToken: c.accessToken);
      if (widget.userId == null) {
        try {
          await ref.read(authServiceProvider).fetchMe(c.serverUrl, c.accessToken);
        } on DioException catch (e) {
          if (e.response?.statusCode != 401) rethrow;
          await ref.read(authProvider.notifier).logout();
          return;
        }
      }
      await ref.read(authProvider.notifier).refreshUser();
      if (mounted) await _load();
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) =>
      Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        const Divider(height: 40),
        const Text('Plex',
            style: TextStyle(fontSize: 20, fontWeight: FontWeight.bold)),
        if (_error != null) Text(_error!),
        if (_identities == null && _error == null)
          const LinearProgressIndicator(),
        if (_identities?.isEmpty == true)
          const Text('No Plex sign-in identity is linked.'),
        for (final p in _identities ?? <Map<String, dynamic>>[])
          Card(
              child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(p['username']?.toString() ?? 'Plex account'),
                        Text(p['email']?.toString() ?? ''),
                        SelectableText('Plex ID: ${p['plex_account_id']}'),
                        TextButton(
                            onPressed: _busy ? null : _unlink,
                            child: const Text('Unlink Plex')),
                      ]))),
        if (widget.userId == null && _available && _identities?.isEmpty == true)
          FilledButton.icon(
              onPressed: _busy
                  ? null
                  : () {
                      final c = ref.read(authProvider).valueOrNull!.connection!;
                      context.go(Uri(path: '/plex/continue', queryParameters: {
                        'server': c.serverUrl,
                        'purpose': 'link'
                      }).toString());
                    },
              icon: const Icon(Icons.link),
              label: const Text('Link with Plex')),
      ]);
}
