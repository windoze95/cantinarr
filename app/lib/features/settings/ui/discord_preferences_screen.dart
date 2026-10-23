import 'settings_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:dio/dio.dart';
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
  ConsumerState<DiscordPreferencesScreen> createState() =>
      _DiscordPreferencesScreenState();
}

class _DiscordPreferencesScreenState
    extends ConsumerState<DiscordPreferencesScreen> {
  final _ids = TextEditingController();
  final _draft = SettingsDraft();
  DiscordPreferences? _prefs;
  Map<String, bool> _events = {};
  bool _enabled = false;
  bool _busy = false;
  String? _error;
  DiscordDelivery? _result;

  Object get _values => [_enabled, _ids.text, _events];
  DiscordPreferencesService get _service =>
      DiscordPreferencesService(ref.read(backendClientProvider));

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    _ids.dispose();
    super.dispose();
  }

  void _saved(DiscordPreferences prefs) {
    _prefs = prefs;
    _enabled = prefs.enabled;
    _ids.text = prefs.discordIds.join(', ');
    // Explicit keys keep a toggle-and-restore from reading as a change.
    _events = {
      for (final key in {...discordEventLabels.keys, ...prefs.events.keys})
        key: prefs.events[key] == true,
    };
    _draft.markSaved(_values);
  }

  Future<void> _load() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final prefs = await _service.get();
      if (mounted) {
        setState(() {
          if (_prefs == null) {
            _saved(prefs);
          } else {
            _prefs = prefs;
          }
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() => _error = e is DioException &&
                e.response?.statusCode == 404
            ? context.l10n.settingsPersonalDiscordMentionsNeedANewerServerUpdate
            : context.l10n.settingsCouldNotReadDiscordPreferencesTryAgain);
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _save() async {
    final ids = _ids.text
        .split(RegExp(r'[\s,]+'))
        .where((id) => id.isNotEmpty)
        .toSet()
        .toList();
    if (ids.length > 10 ||
        ids.any((id) => !RegExp(r'^[1-9][0-9]{16,19}$').hasMatch(id)) ||
        (_enabled && ids.isEmpty)) {
      setState(
          () => _error = context.l10n.settingsEnterUpTo10NumericDiscordUserIds);
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
      _result = null;
    });
    try {
      final prefs = await _service.save(_enabled, ids, _events);
      if (!mounted) return;
      setState(() => _saved(prefs));
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(context.l10n.settingsSaved)));
    } catch (_) {
      if (mounted)
        setState(() => _error =
            context.l10n.settingsCouldNotSaveDiscordPreferencesCheckYourIds);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _test() async {
    setState(() {
      _busy = true;
      _error = null;
      _result = null;
    });
    try {
      final result = await _service.test();
      if (mounted) setState(() => _result = result);
    } catch (_) {
      if (mounted)
        setState(() => _error =
            context.l10n.settingsCouldNotTestMentionsCheckTheServerSettings);
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
        appBar: AppBar(
            title: Text(context.l10n.settingsDiscordNotifications),
            actions: [
              IconButton(
                  tooltip: context.l10n.settingsRefreshDiscordStatus,
                  onPressed: _busy ? null : _load,
                  icon: const Icon(Icons.refresh)),
            ]),
        body: Align(
            alignment: Alignment.topCenter,
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 700),
              child: ListView(padding: const EdgeInsets.all(20), children: [
                if (_busy) const LinearProgressIndicator(),
                Text(context.l10n.settingsGetAPersonalPingInYourServerS),
                if (admin)
                  ListTile(
                    contentPadding: EdgeInsets.zero,
                    title:
                        Text(context.l10n.settingsServerDiscordNotifications),
                    subtitle: Text(context.l10n
                        .settingsConfigureTheWebhookEventsRolesAndAppearance),
                    trailing: const Icon(Icons.chevron_right),
                    onTap: () async {
                      await context
                          .push('/settings/discord-notifications/server');
                      if (mounted) _load();
                    },
                  ),
                if (_error != null)
                  Padding(
                      padding: const EdgeInsets.symmetric(vertical: 12),
                      child: Text(_error!,
                          style: TextStyle(
                              color: Theme.of(context).colorScheme.error))),
                if (_prefs != null) ...[
                  if (_prefs!.blockedReason != null)
                    Padding(
                        padding: const EdgeInsets.symmetric(vertical: 12),
                        child: Text(_prefs!.blockedReason!)),
                  SwitchListTile.adaptive(
                    contentPadding: EdgeInsets.zero,
                    title: Text(context.l10n.settingsMentionMeInDiscord),
                    value: _enabled,
                    onChanged: _busy
                        ? null
                        : (value) => setState(() => _enabled = value),
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: _ids,
                    enabled: !_busy,
                    autocorrect: false,
                    enableSuggestions: false,
                    onChanged: (_) => setState(() => _result = null),
                    decoration: InputDecoration(
                        labelText: context.l10n.settingsDiscordUserIds,
                        border: OutlineInputBorder(),
                        helperText: context.l10n
                            .settingsSeparateMultipleIdsWithCommasUseIdsNot,
                        helperMaxLines: 2),
                  ),
                  const SizedBox(height: 12),
                  Text(context.l10n
                      .settingsInDiscordEnableUserSettingsAdvancedDeveloperMode),
                  const SizedBox(height: 16),
                  for (final entry in discordEventLabels.entries)
                    if (admin || !discordAdminEvents.contains(entry.key))
                      SwitchListTile.adaptive(
                        contentPadding: EdgeInsets.zero,
                        title:
                            Text(settingsDiscordEventLabel(context, entry.key)),
                        subtitle: _prefs!.allowedEvents[entry.key] != true
                            ? Text(context
                                .l10n.settingsThisEventIsDisabledByTheServer)
                            : null,
                        value: _events[entry.key] == true,
                        onChanged: _busy
                            ? null
                            : (value) => setState(() {
                                  _events = {..._events, entry.key: value};
                                }),
                      ),
                  const SizedBox(height: 12),
                  Wrap(spacing: 12, runSpacing: 12, children: [
                    FilledButton(
                        onPressed: _busy ? null : _save,
                        child: Text(context.l10n.settingsSave)),
                    OutlinedButton(
                        onPressed: _busy ||
                                _draft.hasChanges(_values) ||
                                _prefs!.blockedReason != null
                            ? null
                            : _test,
                        child: Text(context.l10n.settingsTestMyMentions)),
                  ]),
                  const SizedBox(height: 12),
                  Text(context.l10n
                      .settingsSaveYourChoicesBeforeTestingTheTestMentions),
                  if (_result != null)
                    ListTile(
                        contentPadding: EdgeInsets.zero,
                        title: Text(context.l10n.settingsMessageTestValue1(
                            (settingsModelLabel(context, _result!.label))
                                .toString())),
                        subtitle: Text(_result!.detail)),
                ],
              ]),
            )),
      ),
    );
  }
}
