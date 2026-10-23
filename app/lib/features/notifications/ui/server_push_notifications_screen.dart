import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/widgets/settings_highlight.dart';
import '../notification_categories.dart';
import '../notification_prefs.dart';
import '../notification_prefs_service.dart';

class ServerPushNotificationsScreen extends ConsumerStatefulWidget {
  const ServerPushNotificationsScreen({super.key, this.highlightId});
  final String? highlightId;

  @override
  ConsumerState<ServerPushNotificationsScreen> createState() =>
      _ServerPushNotificationsScreenState();
}

class _ServerPushNotificationsScreenState
    extends ConsumerState<ServerPushNotificationsScreen> {
  PushNotificationPolicy? _policy;
  bool _saving = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    try {
      final policy =
          await ref.read(notificationPrefsServiceProvider).getServerPolicy();
      if (mounted) {
        setState(() {
          _policy = policy;
          _error = null;
        });
      }
    } catch (_) {
      if (mounted) {
        setState(() => _error = context.l10n.notificationServerReadError);
      }
    }
  }

  Future<void> _save(PushNotificationPolicy next) async {
    if (_saving) return;
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      final saved = await ref
          .read(notificationPrefsServiceProvider)
          .updateServerPolicy(next);
      if (mounted) setState(() => _policy = saved);
    } catch (_) {
      if (mounted) {
        setState(() => _error = context.l10n.notificationServerSaveError);
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final policy = _policy;
    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.notificationServerPush)),
      body: CenteredContent(
          child: policy == null
              ? Center(
                  child: _error == null
                      ? const CircularProgressIndicator()
                      : Column(mainAxisSize: MainAxisSize.min, children: [
                          Text(_error!, textAlign: TextAlign.center),
                          TextButton(
                              onPressed: _load,
                              child: Text(context.l10n.settingsRetry)),
                        ]))
              : ListView(
                  cacheExtent:
                      SettingsHighlight.cacheExtentFor(widget.highlightId),
                  padding: const EdgeInsets.symmetric(vertical: 16),
                  children: [
                    if (_saving) const LinearProgressIndicator(),
                    Padding(
                        padding: EdgeInsets.symmetric(horizontal: 16),
                        child: Text(context.l10n.notificationServerPolicyHelp)),
                    SettingsHighlight(
                        anchorId: 'push-server.enabled',
                        highlightId: widget.highlightId,
                        child: SwitchListTile(
                          title: Text(context.l10n.notificationAllowPush),
                          subtitle:
                              Text(context.l10n.notificationAppliesEveryone),
                          value: policy.enabled,
                          onChanged: _saving
                              ? null
                              : (value) => _save(PushNotificationPolicy(
                                  enabled: value,
                                  categories: policy.categories)),
                        )),
                    if (_error != null)
                      Padding(
                          padding: const EdgeInsets.all(16),
                          child: Text(_error!,
                              style: TextStyle(
                                  color: Theme.of(context).colorScheme.error))),
                    if (!policy.enabled)
                      Padding(
                          padding: EdgeInsets.all(16),
                          child: Text(context.l10n.notificationServerOffSaved)),
                    for (final admin in [false, true]) ...[
                      Padding(
                          padding: const EdgeInsets.fromLTRB(16, 20, 16, 8),
                          child: Text(
                              admin
                                  ? context.l10n.notificationAdministrators
                                  : context.l10n.notificationEveryone,
                              style: Theme.of(context).textTheme.titleMedium)),
                      for (final category
                          in pushCategories.where((c) => c.admin == admin))
                        SettingsHighlight(
                            anchorId:
                                'push-server.${category.key.replaceAll('_', '-')}',
                            highlightId: widget.highlightId,
                            child: SwitchListTile(
                              title: Text(
                                  category.localizedServerTitle(context.l10n)),
                              value: policy.categories[category.key] ?? false,
                              onChanged: _saving || !policy.enabled
                                  ? null
                                  : (value) => _save(PushNotificationPolicy(
                                          enabled: policy.enabled,
                                          categories: {
                                            ...policy.categories,
                                            category.key: value
                                          })),
                            )),
                    ],
                  ],
                )),
    );
  }
}
