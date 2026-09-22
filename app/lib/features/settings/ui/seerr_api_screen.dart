import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';
import '../data/seerr_api_service.dart';

/// Admin screen for the Seerr-compatible API: the address and key another
/// app enters where it asks for Seerr, Overseerr, or Jellyseerr. One key,
/// issued on demand, replaced or revoked here.
class SeerrApiScreen extends ConsumerStatefulWidget {
  const SeerrApiScreen({super.key});

  @override
  ConsumerState<SeerrApiScreen> createState() => _SeerrApiScreenState();
}

class _SeerrApiScreenState extends ConsumerState<SeerrApiScreen> {
  SeerrApiKey? _key;
  bool _busy = false;
  bool _reveal = false;
  String? _error;

  SeerrApiService get _service => ref.read(seerrApiServiceProvider);

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _run(Future<SeerrApiKey> Function() action,
      {String? failure, String? done}) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final key = await action();
      if (!mounted) return;
      setState(() {
        _key = key;
        _reveal = false;
      });
      if (done != null) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(done)));
      }
    } catch (_) {
      if (mounted) {
        setState(() => _error =
            failure ?? 'Could not read the Seerr-compatible API key. Try again.');
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _load() => _run(_service.get);

  Future<void> _issue() async {
    final replacing = _key?.configured == true;
    if (replacing) {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Replace the key?'),
          content: const Text(
              'Apps using the current key stop working until you paste the new one into them.'),
          actions: [
            TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: const Text('Cancel')),
            FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: const Text('Replace key')),
          ],
        ),
      );
      if (confirmed != true) return;
    }
    await _run(_service.issue,
        failure: 'Could not issue a key. Try again.',
        done: replacing ? 'Key replaced' : 'Key issued');
  }

  Future<void> _revoke() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Revoke the key?'),
        content: const Text(
            'Every app using it loses access at once. You can issue a new key any time.'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('Cancel')),
          FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('Revoke key')),
        ],
      ),
    );
    if (confirmed != true) return;
    await _run(_service.revoke,
        failure: 'Could not revoke the key. Try again.', done: 'Key revoked');
  }

  Future<void> _copy(String label, String value) async {
    await Clipboard.setData(ClipboardData(text: value));
    if (!mounted) return;
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text('$label copied')));
  }

  @override
  Widget build(BuildContext context) {
    final serverUrl =
        ref.watch(authProvider).valueOrNull?.connection?.serverUrl ?? '';
    final key = _key;
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(title: const Text('Seerr-compatible API')),
      body: Align(
        alignment: Alignment.topCenter,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 700),
          child: ListView(padding: const EdgeInsets.all(20), children: [
            if (_busy) const LinearProgressIndicator(),
            const Text(
                'Some apps read requests from Seerr, Overseerr, or Jellyseerr: Maintainerr uses them to know who asked for a title before it cleans up, Homepage shows request counts, Dashbrr lists and approves them. Cantinarr answers the same API, so those apps can read its requests instead.'),
            const SizedBox(height: 12),
            const Text(
                'Where the app asks for a Seerr address and API key, enter the address it can reach this server at and the key below. The app adds /api/v1 itself. The key acts as the administrator who issued it and can read every movie and TV request, approve or decline waiting ones, and delete requests. Books and music are not included.'),
            const SizedBox(height: 20),
            Text('Address', style: theme.textTheme.titleMedium),
            const SizedBox(height: 4),
            SelectableText(serverUrl.isNotEmpty
                ? serverUrl
                : 'The address this app is connected with is not known yet.'),
            if (serverUrl.isNotEmpty)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                    onPressed: () => _copy('Address', serverUrl),
                    icon: const Icon(Icons.copy),
                    label: const Text('Copy address')),
              ),
            const SizedBox(height: 4),
            Text(
                'This is the address your app uses. An app on another machine or in another container may need a different one, such as the server\'s LAN address or its container name.',
                style: theme.textTheme.bodySmall),
            const SizedBox(height: 20),
            Text('API key', style: theme.textTheme.titleMedium),
            const SizedBox(height: 4),
            if (_error != null)
              Padding(
                  padding: const EdgeInsets.only(bottom: 12),
                  child: Text(_error!,
                      style: TextStyle(color: theme.colorScheme.error))),
            if (key != null && !key.configured)
              const Text('No key has been issued. Issue one to let apps connect.'),
            if (key != null && key.configured) ...[
              SelectableText(_reveal ? key.apiKey : '•' * 24),
              const SizedBox(height: 4),
              Text(
                  'Issued by ${key.issuedBy.isEmpty ? 'an administrator' : key.issuedBy}'
                  '${key.createdAt == null ? '' : ' on ${_date(key.createdAt!)}'}.',
                  style: theme.textTheme.bodySmall),
              const SizedBox(height: 8),
              Wrap(spacing: 12, runSpacing: 12, children: [
                FilledButton.icon(
                    onPressed: _busy ? null : () => _copy('API key', key.apiKey),
                    icon: const Icon(Icons.copy),
                    label: const Text('Copy key')),
                OutlinedButton(
                    onPressed:
                        _busy ? null : () => setState(() => _reveal = !_reveal),
                    child: Text(_reveal ? 'Hide key' : 'Show key')),
              ]),
            ],
            const SizedBox(height: 16),
            Wrap(spacing: 12, runSpacing: 12, children: [
              if (key != null && key.configured)
                OutlinedButton(
                    onPressed: _busy ? null : _issue,
                    child: const Text('Replace key'))
              else
                FilledButton(
                    onPressed: _busy || key == null ? null : _issue,
                    child: const Text('Issue key')),
              if (key != null && key.configured)
                TextButton(
                    onPressed: _busy ? null : _revoke,
                    child: const Text('Revoke key')),
            ]),
            const SizedBox(height: 28),
            Text('What the apps see', style: theme.textTheme.titleMedium),
            const SizedBox(height: 8),
            const Text(
                'Availability is read live from Radarr and Sonarr each time, so a title removed from a library reads as gone at once and its request can be made again. A request an app deletes is removed from the requester\'s history too, and any allowance it used is released. If a library cannot be reached, the API answers with an error rather than a shorter list; apps built for Seerr treat that as a temporary failure and try again.'),
          ]),
        ),
      ),
    );
  }

  static String _date(DateTime at) {
    final local = at.toLocal();
    String two(int n) => n.toString().padLeft(2, '0');
    return '${local.year}-${two(local.month)}-${two(local.day)}';
  }
}
