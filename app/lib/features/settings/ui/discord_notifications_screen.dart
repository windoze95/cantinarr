import 'settings_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
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

  Map<String, bool> get _selectedEvents =>
      {..._events, 'request_auto_approved': _includeAutoApproved};
  Map<String, dynamic> get _options => {
        'events': _selectedEvents,
        'enable_mentions': _mentions,
        'embed_poster': _poster,
        'role_id': _role.text.trim(),
        'thread_id': _thread.text.trim(),
        'username': _username.text.trim(),
        'avatar_url': _avatar.text.trim(),
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
      for (final key in {
        ...discordEventLabels.keys,
        ...settings.roleEvents.keys
      })
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
        setState(() => _error =
            e is DioException && e.response?.statusCode == 404
                ? context.l10n
                    .settingsDiscordNotificationsNeedANewerServerUpdateCantinarr
                : context
                    .l10n.settingsCouldNotReadDiscordSettingsAndDeliveryStatus);
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
      setState(() => _error =
          context.l10n.settingsAddAWebhookUrlBeforeEnablingNotifications);
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
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text(remove
              ? context.l10n.settingsWebhookRemoved
              : context.l10n.settingsSaved)));
    } catch (_) {
      if (mounted) {
        setState(() => _error =
            context.l10n.settingsCouldNotSaveDiscordSettingsCheckTheWebhook);
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
        setState(() =>
            _error = context.l10n.settingsCouldNotTestTheWebhookCheckTheUrl);
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
          decoration: InputDecoration(
              labelText: label,
              helperText: hint,
              helperMaxLines: 3,
              border: const OutlineInputBorder()),
        ),
      );

  @override
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () => _draft.hasChanges(_values),
        isSaving: _busy,
        child: Scaffold(
          appBar: AppBar(
              title: Text(context.l10n.settingsServerDiscordNotifications),
              actions: [
                IconButton(
                    tooltip: context.l10n.settingsRefreshDeliveryStatus,
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
                      ? context.l10n
                          .settingsChooseWhichRequestAndProblemReportUpdatesAppear
                      : context
                          .l10n.settingsSendNewMediaRequestsNeedingApprovalToA),
                  const SizedBox(height: 12),
                  Text(context.l10n
                      .settingsEnablingThisSharesMediaTitlesMediaTypesRequester),
                  SwitchListTile.adaptive(
                    contentPadding: EdgeInsets.zero,
                    title:
                        Text(context.l10n.settingsSendNotificationsToDiscord),
                    value: _enabled,
                    onChanged: _busy
                        ? null
                        : (value) => setState(() => _enabled = value),
                  ),
                  const SizedBox(height: 12),
                  Text(context
                      .l10n.settingsInYourDiscordTextChannelOpenEditChannel),
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
                          ? context.l10n.settingsReplaceWebhookUrl
                          : context.l10n.settingsWebhookUrl,
                      helperText: _settings!.hasWebhook
                          ? context.l10n.settingsAWebhookIsSavedLeaveBlankToKeep
                          : context.l10n.settingsPasteTheDiscordHttpsWebhookUrl,
                      helperMaxLines: 2,
                      border: const OutlineInputBorder(),
                    ),
                  ),
                  const SizedBox(height: 16),
                  if (!_modern) ...[
                    SwitchListTile.adaptive(
                      contentPadding: EdgeInsets.zero,
                      title: Text(context
                          .l10n.settingsIncludeAutomaticallyApprovedRequests),
                      subtitle: Text(context
                          .l10n.settingsAlsoNotifyTheChannelWhenNoReviewIs),
                      value: _includeAutoApproved,
                      onChanged: _busy || !_enabled
                          ? null
                          : (value) =>
                              setState(() => _includeAutoApproved = value),
                    ),
                    Text(context.l10n
                        .settingsPersonalMentionsMoreEventsThreadsAndAppearanceOptions),
                  ] else ...[
                    ExpansionTile(
                      key: const PageStorageKey('discord-events'),
                      tilePadding: EdgeInsets.zero,
                      title: Text(context.l10n.settingsEvents),
                      subtitle: Text(context.l10n
                          .settingsMessageValue1EventCategoriesSelected(
                              (_selectedEvents.values
                                      .where((value) => value)
                                      .length)
                                  .toString())),
                      children: [
                        for (final entry in discordEventLabels.entries)
                          SwitchListTile.adaptive(
                            contentPadding: EdgeInsets.zero,
                            title: Text(entry.key == 'request_auto_approved'
                                ? context.l10n
                                    .settingsIncludeAutomaticallyApprovedRequests
                                : entry.value),
                            subtitle: entry.key == 'request_available'
                                ? Text(context.l10n
                                    .settingsTvEpisodesAreGroupedOver60SecondsExisting)
                                : null,
                            value: entry.key == 'request_auto_approved'
                                ? _includeAutoApproved
                                : _events[entry.key] == true,
                            onChanged: _busy || !_enabled
                                ? null
                                : (value) => setState(() {
                                      if (entry.key ==
                                          'request_auto_approved') {
                                        _includeAutoApproved = value;
                                      } else {
                                        _events = {
                                          ..._events,
                                          entry.key: value
                                        };
                                      }
                                    }),
                          ),
                      ],
                    ),
                    SwitchListTile.adaptive(
                      contentPadding: EdgeInsets.zero,
                      title: Text(context.l10n.settingsAllowDiscordMentions),
                      subtitle: Text(context
                          .l10n.settingsUsersMustOptInAndAddTheirDiscord),
                      value: _mentions,
                      onChanged: _busy
                          ? null
                          : (value) => setState(() => _mentions = value),
                    ),
                    ExpansionTile(
                      key: const PageStorageKey('discord-roles'),
                      tilePadding: EdgeInsets.zero,
                      title: Text(context.l10n.settingsRoleMentions),
                      children: [
                        _field(
                            _role,
                            context.l10n.settingsDiscordRoleId,
                            context.l10n
                                .settingsOptionalEnableDeveloperModeInDiscordToCopy),
                        for (final entry in discordEventLabels.entries)
                          CheckboxListTile(
                            title: Text(
                                settingsDiscordEventLabel(context, entry.key)),
                            value: _roleEvents[entry.key] == true,
                            onChanged: _busy || !_mentions
                                ? null
                                : (value) => setState(() {
                                      _roleEvents = {
                                        ..._roleEvents,
                                        entry.key: value == true
                                      };
                                    }),
                          ),
                      ],
                    ),
                    ExpansionTile(
                      key: const PageStorageKey('discord-appearance'),
                      tilePadding: EdgeInsets.zero,
                      title: Text(context.l10n.settingsThreadAndAppearance),
                      children: [
                        _field(
                            _thread,
                            context.l10n.settingsDiscordThreadId,
                            context.l10n
                                .settingsOptionalUseAThreadInTheWebhookChannel),
                        _field(_username, context.l10n.settingsDisplayName,
                            context.l10n.settingsLeaveBlankToUseTheWebhookName),
                        _field(_avatar, context.l10n.settingsAvatarUrl,
                            context.l10n.settingsOptionalPublicHttpsImageUrl),
                        SwitchListTile.adaptive(
                          title: Text(context.l10n.settingsIncludePosters),
                          subtitle: Text(context
                              .l10n.settingsShowMovieAndTvArtworkWhenAvailable),
                          value: _poster,
                          onChanged: _busy
                              ? null
                              : (value) => setState(() => _poster = value),
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
                        child: Text(context.l10n.settingsSave)),
                    OutlinedButton(
                        onPressed: _busy ||
                                (!_settings!.hasWebhook &&
                                    _webhook.text.trim().isEmpty)
                            ? null
                            : _test,
                        child: Text(context.l10n.settingsSendTestMessage)),
                    if (_settings!.hasWebhook)
                      TextButton(
                          onPressed: _busy ? null : () => _save(remove: true),
                          child: Text(context.l10n.settingsRemoveWebhook)),
                  ]),
                  const SizedBox(height: 12),
                  Text(_modern
                      ? context
                          .l10n.settingsTestSendsASampleMessageUsingTheEntered
                      : context.l10n
                          .settingsTestSendsASampleMessageUsingTheEntered2),
                  if (_testResult != null)
                    ListTile(
                        contentPadding: EdgeInsets.zero,
                        title: Text(context.l10n.settingsMessageTestValue1(
                            (settingsModelLabel(context, _testResult!.label))
                                .toString())),
                        subtitle: Text(_testResult!.detail)),
                  const SizedBox(height: 28),
                  Text(context.l10n.settingsRecentDeliveries,
                      style: Theme.of(context).textTheme.titleLarge),
                  const SizedBox(height: 8),
                  Text(context.l10n
                      .settingsDiscordFailuresDoNotAffectMediaRequestsUnconfirmed),
                  if (_settings!.error != null)
                    Text(_settings!.error!,
                        style: TextStyle(
                            color: Theme.of(context).colorScheme.error)),
                  if (_settings!.recent.isEmpty)
                    Padding(
                        padding: EdgeInsets.symmetric(vertical: 16),
                        child: Text(context.l10n
                            .settingsNoNotificationsHaveBeenQueuedOnlySelectedEvents)),
                  for (final delivery in _settings!.recent)
                    ListTile(
                      contentPadding: EdgeInsets.zero,
                      title: Text(
                          '${settingsDiscordSubject(context, delivery)} · ${settingsModelLabel(context, delivery.label)}'),
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
