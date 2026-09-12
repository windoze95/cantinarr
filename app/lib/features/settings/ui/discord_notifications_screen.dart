import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../data/discord_notifications_service.dart';

class DiscordNotificationsScreen extends ConsumerStatefulWidget {
  const DiscordNotificationsScreen({super.key});

  @override
  ConsumerState<DiscordNotificationsScreen> createState() =>
      _DiscordNotificationsScreenState();
}

class _DiscordNotificationsScreenState
    extends ConsumerState<DiscordNotificationsScreen> {
  final _webhook = TextEditingController();
  final _draft = SettingsDraft();
  DiscordNotificationSettings? _settings;
  DiscordDelivery? _testResult;
  bool _enabled = false;
  bool _includeAutoApproved = false;
  bool _busy = false;
  String? _error;

  Object get _values => [_enabled, _includeAutoApproved, _webhook.text];
  DiscordNotificationsService get _service =>
      DiscordNotificationsService(ref.read(backendClientProvider));

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    _webhook.dispose();
    super.dispose();
  }

  void _saved(DiscordNotificationSettings settings) {
    _settings = settings;
    _enabled = settings.enabled;
    _includeAutoApproved = settings.includeAutoApproved;
    _webhook.clear();
    _draft.markSaved(_values);
  }

  Future<void> _load() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final settings = await _service.get();
      if (!mounted) return;
      setState(() {
        if (_settings == null) {
          _saved(settings);
        } else {
          _settings = settings;
        }
      });
    } catch (_) {
      if (mounted) {
        setState(() => _error =
            'Could not read Discord settings and delivery status. Try again.');
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _save({bool remove = false}) async {
    if (_busy) return;
    if (!remove &&
        _enabled &&
        _webhook.text.trim().isEmpty &&
        _settings?.hasWebhook != true) {
      setState(
          () => _error = 'Add a webhook URL before enabling notifications.');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final settings = remove
          ? await _service.remove()
          : await _service.save(_enabled, _webhook.text, _includeAutoApproved);
      if (!mounted) return;
      setState(() {
        _saved(settings);
        _testResult = null;
      });
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(remove ? 'Webhook removed' : 'Saved')));
    } catch (_) {
      if (mounted) {
        setState(() => _error =
            'Could not save Discord settings. Check the webhook URL and try again.');
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _test() async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
      _testResult = null;
    });
    try {
      final result = await _service.test(_webhook.text);
      if (mounted) setState(() => _testResult = result);
    } catch (_) {
      if (mounted) {
        setState(() => _error =
            'Could not test the webhook. Check the URL and try again.');
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () => _draft.hasChanges(_values),
        isSaving: _busy,
        child: Scaffold(
          appBar: AppBar(title: const Text('Discord Notifications'), actions: [
            IconButton(
                tooltip: 'Refresh delivery status',
                onPressed: _busy ? null : _load,
                icon: const Icon(Icons.refresh)),
          ]),
          body: Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 700),
              child: ListView(padding: const EdgeInsets.all(20), children: [
                if (_busy) const LinearProgressIndicator(),
                if (_error != null && _settings == null)
                  Padding(
                      padding: const EdgeInsets.symmetric(vertical: 12),
                      child: Text(_error!,
                          style: TextStyle(
                              color: Theme.of(context).colorScheme.error))),
                if (_settings != null) ...[
                  const Text(
                      'Send new media requests needing approval to a Discord text channel. You can also include requests that need no review.'),
                  const SizedBox(height: 12),
                  const Text(
                      'Enabling this shares media titles, media types, requester usernames, and approval states with Discord and everyone who can read the channel. If External Address is configured, messages also include a link to Cantinarr.'),
                  SwitchListTile.adaptive(
                    contentPadding: EdgeInsets.zero,
                    title: const Text('Send new requests to Discord'),
                    value: _enabled,
                    onChanged: _busy
                        ? null
                        : (value) => setState(() => _enabled = value),
                  ),
                  SwitchListTile.adaptive(
                    contentPadding: EdgeInsets.zero,
                    title:
                        const Text('Include automatically approved requests'),
                    subtitle: const Text(
                        'Also notify the channel when no review is needed.'),
                    value: _includeAutoApproved,
                    onChanged: _busy || !_enabled
                        ? null
                        : (value) =>
                            setState(() => _includeAutoApproved = value),
                  ),
                  const SizedBox(height: 12),
                  const Text(
                      'In your Discord text channel, open Edit Channel → Integrations → Webhooks, create a webhook, and copy its URL.'),
                  const SizedBox(height: 16),
                  TextField(
                    controller: _webhook,
                    enabled: !_busy,
                    obscureText: true,
                    autocorrect: false,
                    enableSuggestions: false,
                    onChanged: (_) => setState(() => _testResult = null),
                    decoration: InputDecoration(
                      labelText: _settings!.hasWebhook
                          ? 'Replace webhook URL'
                          : 'Webhook URL',
                      helperText: _settings!.hasWebhook
                          ? 'A webhook is saved. Leave blank to keep it.'
                          : 'Paste the Discord HTTPS webhook URL.',
                      helperMaxLines: 2,
                      border: const OutlineInputBorder(),
                    ),
                  ),
                  const SizedBox(height: 16),
                  if (_error != null)
                    Padding(
                        padding: const EdgeInsets.only(bottom: 12),
                        child: Text(_error!,
                            style: TextStyle(
                                color: Theme.of(context).colorScheme.error))),
                  Wrap(spacing: 12, runSpacing: 12, children: [
                    FilledButton(
                        onPressed: _busy ? null : () => _save(),
                        child: const Text('Save')),
                    OutlinedButton(
                        onPressed: _busy ||
                                (!_settings!.hasWebhook &&
                                    _webhook.text.trim().isEmpty)
                            ? null
                            : _test,
                        child: const Text('Send test message')),
                    if (_settings!.hasWebhook)
                      TextButton(
                          onPressed: _busy ? null : () => _save(remove: true),
                          child: const Text('Remove webhook')),
                  ]),
                  const SizedBox(height: 12),
                  const Text(
                      'Test sends a sample message using the entered URL, or the saved webhook if blank. It does not save your changes or enable notifications.'),
                  if (_testResult != null)
                    ListTile(
                        contentPadding: EdgeInsets.zero,
                        title: Text('Test: ${_testResult!.label}'),
                        subtitle: Text(_testResult!.detail)),
                  const SizedBox(height: 28),
                  Text('Recent deliveries',
                      style: Theme.of(context).textTheme.titleLarge),
                  const SizedBox(height: 8),
                  const Text(
                      'Discord failures do not affect media requests. Unconfirmed messages are not resent automatically to avoid duplicate alerts. Disabling or replacing the webhook cancels messages waiting to send.'),
                  if (_settings!.error != null)
                    Text(_settings!.error!,
                        style: TextStyle(
                            color: Theme.of(context).colorScheme.error)),
                  if (_settings!.recent.isEmpty)
                    const Padding(
                        padding: EdgeInsets.symmetric(vertical: 16),
                        child: Text(
                            'No request alerts have been queued. Only new requests created while notifications are enabled are sent.')),
                  for (final delivery in _settings!.recent)
                    ListTile(
                      contentPadding: EdgeInsets.zero,
                      title: Text(
                          'Request #${delivery.requestId} · ${delivery.label}'),
                      subtitle: Text(
                          '${delivery.detail}${delivery.updatedAt == null ? '' : '\n${delivery.updatedAt}'}'),
                    ),
                ],
              ]),
            ),
          ),
        ),
      );
}
