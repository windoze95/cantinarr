import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
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
                    'A provider identity links only after you sign in to both Cantinarr and the provider. Matching names or email addresses never links accounts.'),
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
                if (_status?.ssoOnly == true)
                  const Padding(
                      padding: EdgeInsets.only(top: 16),
                      child: Text(
                          'This server requires single sign-on for regular users. Administrators retain local recovery access.')),
              ]),
            )),
      );
}
