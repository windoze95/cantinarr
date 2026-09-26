import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/discord_notifications_service.dart';

class DiscordPreferencesScreen extends ConsumerStatefulWidget {
  const DiscordPreferencesScreen({super.key});

  @override
  ConsumerState<DiscordPreferencesScreen> createState() => _DiscordPreferencesScreenState();
}

class _DiscordPreferencesScreenState extends ConsumerState<DiscordPreferencesScreen> {
  final _ids = TextEditingController();
  final _draft = SettingsDraft();
  DiscordPreferences? _prefs;
  Map<String, bool> _events = {};
  bool _enabled = false;
  bool _busy = false;
  String? _error;
  DiscordDelivery? _result;

  Object get _values => [_enabled, _ids.text, _events];
  DiscordPreferencesService get _service => DiscordPreferencesService(ref.read(backendClientProvider));

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() { _ids.dispose(); super.dispose(); }

  void _saved(DiscordPreferences prefs) {
    _prefs = prefs;
    _enabled = prefs.enabled;
    _ids.text = prefs.discordIds.join(', ');
    _events = Map.of(prefs.events);
    _draft.markSaved(_values);
  }

  Future<void> _load() async {
    setState(() { _busy = true; _error = null; });
    try {
      final prefs = await _service.get();
      if (mounted) {
        setState(() {
          if (_prefs == null) { _saved(prefs); } else { _prefs = prefs; }
        });
      }
    } catch (_) {
      if (mounted) setState(() => _error = 'Could not read Discord preferences. Try again.');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _save() async {
    final ids = _ids.text.split(RegExp(r'[\s,]+')).where((id) => id.isNotEmpty).toSet().toList();
    if (ids.length > 10 || ids.any((id) => !RegExp(r'^[1-9][0-9]{16,19}$').hasMatch(id)) || (_enabled && ids.isEmpty)) {
      setState(() => _error = 'Enter up to 10 numeric Discord user IDs before enabling mentions.');
      return;
    }
    setState(() { _busy = true; _error = null; _result = null; });
    try {
      final prefs = await _service.save(_enabled, ids, _events);
      if (!mounted) return;
      setState(() => _saved(prefs));
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Saved')));
    } catch (_) {
      if (mounted) setState(() => _error = 'Could not save Discord preferences. Check your IDs and try again.');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _test() async {
    setState(() { _busy = true; _error = null; _result = null; });
    try {
      final result = await _service.test();
      if (mounted) setState(() => _result = result);
    } catch (_) {
      if (mounted) setState(() => _error = 'Could not test mentions. Check the server settings or wait a minute before trying again.');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final admin = ref.watch(authProvider).valueOrNull?.user?.isAdmin == true;
    return UnsavedChangesGuard(
      hasChanges: () => _draft.hasChanges(_values),
      isSaving: _busy,
      child: Scaffold(
        appBar: AppBar(title: const Text('Discord Notifications'), actions: [
          IconButton(tooltip: 'Refresh Discord status', onPressed: _busy ? null : _load,
              icon: const Icon(Icons.refresh)),
        ]),
        body: Align(alignment: Alignment.topCenter, child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 700),
          child: ListView(padding: const EdgeInsets.all(20), children: [
            if (_busy) const LinearProgressIndicator(),
            const Text('Get a personal ping in your server’s Discord channel when your requested content is ready or a report changes. These are channel mentions, not direct messages.'),
            if (admin) ListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('Server Discord Notifications'),
              subtitle: const Text('Configure the webhook, events, roles, and appearance'),
              trailing: const Icon(Icons.chevron_right),
              onTap: () async {
                await context.push('/settings/discord-notifications/server');
                if (mounted) _load();
              },
            ),
            if (_error != null) Padding(padding: const EdgeInsets.symmetric(vertical: 12),
              child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error))),
            if (_prefs != null) ...[
              if (_prefs!.blockedReason != null) Padding(
                padding: const EdgeInsets.symmetric(vertical: 12), child: Text(_prefs!.blockedReason!)),
              SwitchListTile.adaptive(
                contentPadding: EdgeInsets.zero,
                title: const Text('Mention me in Discord'),
                value: _enabled,
                onChanged: _busy ? null : (value) => setState(() => _enabled = value),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _ids,
                enabled: !_busy,
                autocorrect: false,
                enableSuggestions: false,
                onChanged: (_) => setState(() => _result = null),
                decoration: const InputDecoration(
                  labelText: 'Discord user IDs', border: OutlineInputBorder(),
                  helperText: 'Separate multiple IDs with commas. Use IDs, not usernames.', helperMaxLines: 2),
              ),
              const SizedBox(height: 12),
              const Text('In Discord, enable User Settings → Advanced → Developer Mode, then copy your user ID from your profile. The IDs you enter will be visible to people in the notification channel. Your Discord server, channel, and notification settings also control whether you receive a ping.'),
              const SizedBox(height: 16),
              for (final entry in discordEventLabels.entries)
                if (admin || !discordAdminEvents.contains(entry.key))
                  SwitchListTile.adaptive(
                    contentPadding: EdgeInsets.zero,
                    title: Text(entry.value),
                    subtitle: _prefs!.allowedEvents[entry.key] != true
                        ? const Text('This event is disabled by the server.') : null,
                    value: _events[entry.key] == true,
                    onChanged: _busy ? null : (value) => setState(() {
                      _events = {..._events, entry.key: value};
                    }),
                  ),
              const SizedBox(height: 12),
              Wrap(spacing: 12, runSpacing: 12, children: [
                FilledButton(onPressed: _busy ? null : _save, child: const Text('Save')),
                OutlinedButton(
                  onPressed: _busy || _draft.hasChanges(_values) || _prefs!.blockedReason != null ? null : _test,
                  child: const Text('Test my mentions')),
              ]),
              const SizedBox(height: 12),
              const Text('Save your choices before testing. The test mentions only your saved IDs in the configured channel.'),
              if (_result != null) ListTile(contentPadding: EdgeInsets.zero,
                title: Text('Test: ${_result!.label}'), subtitle: Text(_result!.detail)),
            ],
          ]),
        )),
      ),
    );
  }
}
