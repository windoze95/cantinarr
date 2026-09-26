import 'package:dio/dio.dart';
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
  final _role = TextEditingController();
  final _thread = TextEditingController();
  final _username = TextEditingController();
  final _avatar = TextEditingController();
  Map<String, bool> _events = {};
  Map<String, bool> _roleEvents = {};
  bool _mentions = false;
  bool _poster = false;
  final _draft = SettingsDraft();
  DiscordNotificationSettings? _settings;
  DiscordDelivery? _testResult;
  bool _enabled = false;
  bool _includeAutoApproved = false;
  bool _busy = false;
  String? _error;

  Map<String, bool> get _selectedEvents => {..._events, 'request_auto_approved': _includeAutoApproved};
  Map<String, dynamic> get _options => {
    'events': _selectedEvents,
    'enable_mentions': _mentions, 'embed_poster': _poster,
    'role_id': _role.text.trim(), 'thread_id': _thread.text.trim(),
    'username': _username.text.trim(), 'avatar_url': _avatar.text.trim(),
    'role_events': _roleEvents,
  };
  // Older servers reject unknown settings, so only a server that reports
  // events is sent (or shown) the newer options.
  bool get _modern => _settings?.supportsEvents == true;
  Object get _values =>
      [_enabled, _webhook.text, _modern ? _options : _includeAutoApproved];
  DiscordNotificationsService get _service =>
      DiscordNotificationsService(ref.read(backendClientProvider));

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    for (final controller in [_webhook, _role, _thread, _username, _avatar]) {
      controller.dispose();
    }
    super.dispose();
  }

  void _saved(DiscordNotificationSettings settings) {
    _settings = settings;
    _enabled = settings.enabled;
    _includeAutoApproved = settings.includeAutoApproved;
    _webhook.clear();
    // Every known key starts explicit, so toggling one back off leaves no
    // draft. Keys from a newer server are kept and sent back unchanged.
    _events = {
      for (final key in {...discordEventLabels.keys, ...settings.events.keys})
        key: settings.events[key] == true,
    };
    _roleEvents = {
      for (final key in {...discordEventLabels.keys, ...settings.roleEvents.keys})
        key: settings.roleEvents[key] == true,
    };
    _mentions = settings.enableMentions;
    _poster = settings.embedPoster;
    _role.text = settings.roleId;
    _thread.text = settings.threadId;
    _username.text = settings.username;
    _avatar.text = settings.avatarUrl;
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
    } catch (e) {
      if (mounted) {
        setState(() => _error = e is DioException &&
                e.response?.statusCode == 404
            ? 'Discord notifications need a newer server. Update Cantinarr and try again.'
            : 'Could not read Discord settings and delivery status. Try again.');
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
          : await _service.save(_enabled, _webhook.text, _includeAutoApproved,
              options: _modern ? _options : const {});
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
            'Could not save Discord settings. Check the webhook URL, Discord IDs, and appearance settings.');
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
      final result = await _service.test(_webhook.text,
          options: _modern ? _options : const {});
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

  Widget _field(TextEditingController controller, String label, String hint) =>
      Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: TextField(
          key: PageStorageKey('discord-field-$label'),
          controller: controller,
          enabled: !_busy,
          autocorrect: false,
          onChanged: (_) => setState(() => _testResult = null),
          decoration: InputDecoration(labelText: label, helperText: hint,
              helperMaxLines: 3, border: const OutlineInputBorder()),
        ),
      );

  @override
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () => _draft.hasChanges(_values),
        isSaving: _busy,
        child: Scaffold(
          appBar: AppBar(title: const Text('Server Discord Notifications'), actions: [
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
                  Text(_modern
                      ? 'Choose which request and problem-report updates appear in your Discord channel or thread. Users can opt in to personal mentions in Discord Notifications.'
                      : 'Send new media requests needing approval to a Discord text channel. You can also include requests that need no review.'),
                  const SizedBox(height: 12),
                  const Text(
                      'Enabling this shares media titles, media types, requester usernames, library names, and request or report states with Discord and everyone who can read the channel. If External Address is configured, messages also include a link to Cantinarr.'),
                  SwitchListTile.adaptive(
                    contentPadding: EdgeInsets.zero,
                    title: const Text('Send notifications to Discord'),
                    value: _enabled,
                    onChanged: _busy
                        ? null
                        : (value) => setState(() => _enabled = value),
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
                  if (!_modern) ...[
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
                    const Text(
                        'Personal mentions, more events, threads, and appearance options need a newer server. Update Cantinarr to use them.'),
                  ] else ...[
                  ExpansionTile(
                    key: const PageStorageKey('discord-events'),
                    tilePadding: EdgeInsets.zero,
                    title: const Text('Events'),
                    subtitle: Text('${_selectedEvents.values.where((value) => value).length} event categories selected'),
                    children: [
                  for (final entry in discordEventLabels.entries)
                    SwitchListTile.adaptive(
                      contentPadding: EdgeInsets.zero,
                      title: Text(entry.key == 'request_auto_approved'
                          ? 'Include automatically approved requests' : entry.value),
                      subtitle: entry.key == 'request_available'
                          ? const Text('TV episodes are grouped over 60 seconds. Existing available content is not replayed when enabled.') : null,
                      value: entry.key == 'request_auto_approved'
                          ? _includeAutoApproved : _events[entry.key] == true,
                      onChanged: _busy || !_enabled ? null : (value) => setState(() {
                        if (entry.key == 'request_auto_approved') {
                          _includeAutoApproved = value;
                        } else {
                          _events = {..._events, entry.key: value};
                        }
                      }),
                    ),
                    ],
                  ),
                  SwitchListTile.adaptive(
                    contentPadding: EdgeInsets.zero,
                    title: const Text('Allow Discord mentions'),
                    subtitle: const Text('Users must opt in and add their Discord IDs. Only selected roles and users can be pinged.'),
                    value: _mentions,
                    onChanged: _busy ? null : (value) => setState(() => _mentions = value),
                  ),
                  ExpansionTile(
                    key: const PageStorageKey('discord-roles'),
                    tilePadding: EdgeInsets.zero,
                    title: const Text('Role mentions'),
                    children: [
                      _field(_role, 'Discord role ID', 'Optional. Enable Developer Mode in Discord to copy IDs.'),
                      for (final entry in discordEventLabels.entries)
                        CheckboxListTile(
                          title: Text(entry.value),
                          value: _roleEvents[entry.key] == true,
                          onChanged: _busy || !_mentions ? null : (value) => setState(() {
                            _roleEvents = {..._roleEvents, entry.key: value == true};
                          }),
                        ),
                    ],
                  ),
                  ExpansionTile(
                    key: const PageStorageKey('discord-appearance'),
                    tilePadding: EdgeInsets.zero,
                    title: const Text('Thread and appearance'),
                    children: [
                      _field(_thread, 'Discord thread ID', 'Optional. Use a thread in the webhook channel.'),
                      _field(_username, 'Display name', 'Leave blank to use the webhook name.'),
                      _field(_avatar, 'Avatar URL', 'Optional public HTTPS image URL.'),
                      SwitchListTile.adaptive(
                        title: const Text('Include posters'),
                        subtitle: const Text('Show movie and TV artwork when available.'),
                        value: _poster,
                        onChanged: _busy ? null : (value) => setState(() => _poster = value),
                      ),
                    ],
                  ),
                  ],
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
                  Text(_modern
                      ? 'Test sends a sample message using the entered URL, or the saved webhook if blank. It uses the thread and appearance above without saving changes or mentioning anyone.'
                      : 'Test sends a sample message using the entered URL, or the saved webhook if blank. It does not save your changes or enable notifications.'),
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
                            'No notifications have been queued. Only selected events occurring while notifications are enabled are sent.')),
                  for (final delivery in _settings!.recent)
                    ListTile(
                      contentPadding: EdgeInsets.zero,
                      title: Text(
                          '${delivery.subject} · ${delivery.label}'),
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
