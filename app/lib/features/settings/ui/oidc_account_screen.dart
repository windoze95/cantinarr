import 'package:cantinarr/l10n/l10n.dart';
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
                ? context.l10n.settingsThisServerDoesNotSupportSingleSignOn
                : oidcError(e, context: context));
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
      if (mounted) setState(() => _error = oidcError(e, context: context));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _unlink(String issuer) async {
    final approved = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
              title: Text(context.l10n.settingsUnlinkSingleSignOn),
              content:
                  Text(context.l10n.settingsThisSignsOutThisAccountSSsoDevices),
              actions: [
                TextButton(
                    onPressed: () => Navigator.pop(context, false),
                    child: Text(context.l10n.settingsCancel)),
                FilledButton(
                    onPressed: () => Navigator.pop(context, true),
                    child: Text(context.l10n.settingsUnlink))
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
      if (mounted) setState(() => _error = oidcError(e, context: context));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
        appBar: AppBar(title: Text(context.l10n.settingsLinkedSignIn)),
        body: Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 680),
              child: ListView(padding: const EdgeInsets.all(24), children: [
                Text(context.l10n.settingsSignInToCantinarrAndTheProviderTo),
                const SizedBox(height: 16),
                if (_error != null)
                  Text(_error!,
                      style: TextStyle(
                          color: Theme.of(context).colorScheme.error)),
                if (_identities == null && _error == null)
                  const Center(child: CircularProgressIndicator()),
                if (_identities?.isEmpty == true)
                  Text(context.l10n.settingsNoSingleSignOnIdentityIsLinkedTo),
                for (final identity in _identities ?? <Map<String, dynamic>>[])
                  Card(
                      child: Padding(
                    padding: const EdgeInsets.all(16),
                    child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(context.l10n.settingsLinked,
                              style: TextStyle(fontWeight: FontWeight.bold)),
                          SelectableText(identity['issuer'] as String),
                          const SizedBox(height: 8),
                          SelectableText(context.l10n
                              .settingsMessageIdentityValue1(
                                  (identity['subject']).toString())),
                          TextButton(
                              onPressed: _busy
                                  ? null
                                  : () => _unlink(identity['issuer'] as String),
                              child: Text(context.l10n.settingsUnlink)),
                        ]),
                  )),
                if (widget.userId == null && _status?.ssoAvailable == true) ...[
                  const SizedBox(height: 16),
                  FilledButton.icon(
                      onPressed: _busy ? null : _link,
                      icon: const Icon(Icons.link),
                      label: Text(context.l10n.settingsMessageLinkWithValue1(
                          (_status!.ssoProvider).toString()))),
                  const SizedBox(height: 12),
                  Text(context
                      .l10n.settingsYourBrowserWillOpenClosingItLeavesYour),
                ],
                _PlexIdentities(userId: widget.userId),
                if (_status?.ssoOnly == true)
                  Padding(
                      padding: EdgeInsets.only(top: 16),
                      child: Text(context.l10n
                          .settingsThisServerRequiresSingleSignOnForRegular)),
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
            ? context.l10n.settingsThisServerDoesNotSupportPlexSignIn
            : oidcError(e, context: context));
      }
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e, context: context));
    }
  }

  Future<void> _unlink() async {
    final confirmed = await showDialog<bool>(
        context: context,
        builder: (c) => AlertDialog(
                title: Text(context.l10n.settingsUnlinkPlexSignIn),
                content: Text(
                    context.l10n.settingsThisSignsOutPlexDevicesAndMcpClients),
                actions: [
                  TextButton(
                      onPressed: () => Navigator.pop(c, false),
                      child: Text(context.l10n.settingsCancel)),
                  FilledButton(
                      onPressed: () => Navigator.pop(c, true),
                      child: Text(context.l10n.settingsUnlinkPlex))
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
          await ref
              .read(authServiceProvider)
              .fetchMe(c.serverUrl, c.accessToken);
        } on DioException catch (e) {
          if (e.response?.statusCode != 401) rethrow;
          await ref.read(authProvider.notifier).logout();
          return;
        }
      }
      await ref.read(authProvider.notifier).refreshUser();
      if (mounted) await _load();
    } catch (e) {
      if (mounted) setState(() => _error = oidcError(e, context: context));
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
          Text(context.l10n.settingsNoPlexSignInIdentityIsLinked),
        for (final p in _identities ?? <Map<String, dynamic>>[])
          Card(
              child: Padding(
                  padding: const EdgeInsets.all(16),
                  child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(p['username']?.toString() ??
                            context.l10n.settingsPlexAccount),
                        Text(p['email']?.toString() ?? ''),
                        SelectableText(context.l10n.settingsMessagePlexIdValue1(
                            (p['plex_account_id']).toString())),
                        TextButton(
                            onPressed: _busy ? null : _unlink,
                            child: Text(context.l10n.settingsUnlinkPlex)),
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
              label: Text(context.l10n.settingsLinkWithPlex)),
      ]);
}
