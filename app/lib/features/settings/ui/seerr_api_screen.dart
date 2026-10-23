import 'package:cantinarr/l10n/l10n.dart';
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
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(done)));
      }
    } catch (_) {
      if (mounted) {
        setState(() => _error = failure ??
            context.l10n.settingsCouldNotReadTheSeerrCompatibleApiKey);
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
          title: Text(context.l10n.settingsReplaceTheKey),
          content:
              Text(context.l10n.settingsAppsUsingTheCurrentKeyStopWorkingUntil),
          actions: [
            TextButton(
                onPressed: () => Navigator.pop(context, false),
                child: Text(context.l10n.settingsCancel)),
            FilledButton(
                onPressed: () => Navigator.pop(context, true),
                child: Text(context.l10n.settingsReplaceKey)),
          ],
        ),
      );
      if (confirmed != true) return;
    }
    await _run(_service.issue,
        failure: context.l10n.settingsCouldNotIssueAKeyTryAgain,
        done: replacing
            ? context.l10n.settingsKeyReplaced
            : context.l10n.settingsKeyIssued);
  }

  Future<void> _revoke() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(context.l10n.settingsRevokeTheKey),
        content: Text(context.l10n.settingsEveryAppUsingItLosesAccessAtOnce),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: Text(context.l10n.settingsCancel)),
          FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: Text(context.l10n.settingsRevokeKey)),
        ],
      ),
    );
    if (confirmed != true) return;
    await _run(_service.revoke,
        failure: context.l10n.settingsCouldNotRevokeTheKeyTryAgain,
        done: context.l10n.settingsKeyRevoked);
  }

  Future<void> _copy(String label, String value) async {
    await Clipboard.setData(ClipboardData(text: value));
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(
            context.l10n.settingsMessageValue1Copied((label).toString()))));
  }

  @override
  Widget build(BuildContext context) {
    final serverUrl =
        ref.watch(authProvider).valueOrNull?.connection?.serverUrl ?? '';
    final key = _key;
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.settingsSeerrApi)),
      body: Align(
        alignment: Alignment.topCenter,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 700),
          child: ListView(padding: const EdgeInsets.all(20), children: [
            if (_busy) const LinearProgressIndicator(),
            Text(context.l10n.settingsSomeAppsReadRequestsFromSeerrOverseerrOr),
            const SizedBox(height: 12),
            Text(context.l10n.settingsWhereTheAppAsksForASeerrAddress),
            const SizedBox(height: 20),
            Text(context.l10n.settingsAddress,
                style: theme.textTheme.titleMedium),
            const SizedBox(height: 4),
            SelectableText(serverUrl.isNotEmpty
                ? serverUrl
                : context.l10n.settingsTheAddressThisAppIsConnectedWithIs),
            if (serverUrl.isNotEmpty)
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                    onPressed: () =>
                        _copy(context.l10n.settingsAddress, serverUrl),
                    icon: const Icon(Icons.copy),
                    label: Text(context.l10n.settingsCopyAddress)),
              ),
            const SizedBox(height: 4),
            Text(context.l10n.settingsThisIsTheAddressYourAppUsesAn,
                style: theme.textTheme.bodySmall),
            const SizedBox(height: 20),
            Text(context.l10n.settingsApiKey,
                style: theme.textTheme.titleMedium),
            const SizedBox(height: 4),
            if (_error != null)
              Padding(
                  padding: const EdgeInsets.only(bottom: 12),
                  child: Text(_error!,
                      style: TextStyle(color: theme.colorScheme.error))),
            if (key != null && !key.configured)
              Text(context.l10n.settingsNoKeyHasBeenIssuedIssueOneTo),
            if (key != null && key.configured) ...[
              SelectableText(_reveal ? key.apiKey : '•' * 24),
              const SizedBox(height: 4),
              Text(
                  context.l10n.settingsMessageIssuedByValue1Value2(
                      (key.issuedBy.isEmpty
                              ? context.l10n.settingsAnAdministrator
                              : key.issuedBy)
                          .toString(),
                      (key.createdAt == null
                              ? ''
                              : context.l10n.settingsMessageOnValue1(
                                  (_date(key.createdAt!)).toString()))
                          .toString()),
                  style: theme.textTheme.bodySmall),
              const SizedBox(height: 8),
              Wrap(spacing: 12, runSpacing: 12, children: [
                FilledButton.icon(
                    onPressed: _busy
                        ? null
                        : () => _copy(context.l10n.settingsApiKey, key.apiKey),
                    icon: const Icon(Icons.copy),
                    label: Text(context.l10n.settingsCopyKey)),
                OutlinedButton(
                    onPressed:
                        _busy ? null : () => setState(() => _reveal = !_reveal),
                    child: Text(_reveal
                        ? context.l10n.settingsHideKey
                        : context.l10n.settingsShowKey)),
              ]),
            ],
            const SizedBox(height: 16),
            Wrap(spacing: 12, runSpacing: 12, children: [
              if (key != null && key.configured)
                OutlinedButton(
                    onPressed: _busy ? null : _issue,
                    child: Text(context.l10n.settingsReplaceKey))
              else
                FilledButton(
                    onPressed: _busy || key == null ? null : _issue,
                    child: Text(context.l10n.settingsIssueKey)),
              if (key != null && key.configured)
                TextButton(
                    onPressed: _busy ? null : _revoke,
                    child: Text(context.l10n.settingsRevokeKey)),
            ]),
            const SizedBox(height: 28),
            Text(context.l10n.settingsWhatTheAppsSee,
                style: theme.textTheme.titleMedium),
            const SizedBox(height: 8),
            Text(
                context.l10n.settingsAvailabilityIsReadLiveFromRadarrAndSonarr),
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
