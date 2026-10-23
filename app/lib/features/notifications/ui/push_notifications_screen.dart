import 'notification_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/settings_highlight.dart';
import '../../auth/logic/auth_provider.dart';
import '../../settings/settings_anchors.dart';
import '../notification_prefs.dart';
import '../notification_categories.dart';
import '../notification_prefs_service.dart';
import '../push_service.dart';

/// Lets the current user choose which push notifications they receive. Loads
/// the saved preferences on open and persists each toggle immediately,
/// reverting the switch if the server rejects the change.
class PushNotificationsScreen extends ConsumerStatefulWidget {
  /// Settings-search anchor to scroll to and flash on arrival.
  final String? highlightId;

  const PushNotificationsScreen({super.key, this.highlightId});

  @override
  ConsumerState<PushNotificationsScreen> createState() =>
      _PushNotificationsScreenState();
}

class _PushNotificationsScreenState
    extends ConsumerState<PushNotificationsScreen> with WidgetsBindingObserver {
  bool _isLoading = true;
  bool _saving = false;
  String? _error;
  NotificationPrefs? _prefs;

  /// Whether the push status section is shown at all (mobile only — web has
  /// no push registration, just the in-tab WebSocket updates).
  static final bool _pushSupported =
      !kIsWeb && (Platform.isIOS || Platform.isAndroid);

  /// Current OS notification authorization status. Mirrors the strings from
  /// [PushService.authorizationStatus]: `authorized`, `denied`,
  /// `notDetermined`, `provisional`, or `ephemeral`.
  String _authStatus = 'notDetermined';
  bool _sendingTest = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Returning from the system Settings app (where the user may have changed
    // the permission) resumes us; re-read the status so the UI stays accurate.
    if (state == AppLifecycleState.resumed && _pushSupported) {
      if (!_saving) _load();
    }
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : context.l10n.notificationSomethingWrong;
  }

  Future<void> _refreshAuthStatus() async {
    if (!_pushSupported) return;
    final status = await ref.read(pushServiceProvider).authorizationStatus();
    if (!mounted) return;
    setState(() => _authStatus = status);
  }

  Future<void> _load() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final prefs =
          await ref.read(notificationPrefsServiceProvider).getPreferences();
      final status = _pushSupported
          ? await ref.read(pushServiceProvider).authorizationStatus()
          : 'notDetermined';
      if (!mounted) return;
      setState(() {
        _prefs = prefs;
        _authStatus = status;
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = _friendlyError(e);
        _isLoading = false;
      });
    }
  }

  /// Requests permission via the native channel, then refreshes the status.
  Future<void> _enableNotifications() async {
    await ref.read(pushServiceProvider).registerForPush();
    await _refreshAuthStatus();
  }

  Future<void> _sendTest() async {
    setState(() => _sendingTest = true);
    try {
      final result = await ref.read(pushServiceProvider).sendTest();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(localizedDescribePushTest(context, result))),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(_friendlyError(e))));
    } finally {
      if (mounted) setState(() => _sendingTest = false);
    }
  }

  /// Applies [updated] optimistically, then persists it. On failure the UI
  /// reverts to [previous] and surfaces the error.
  Future<void> _save(
      NotificationPrefs updated, NotificationPrefs previous) async {
    if (_saving) return;
    setState(() {
      _prefs = updated;
      _saving = true;
    });
    try {
      final saved = await ref
          .read(notificationPrefsServiceProvider)
          .updatePreferences(updated);
      if (!mounted) return;
      setState(() => _prefs = saved);
      if (!previous.pushEnabled &&
          saved.pushEnabled &&
          saved.serverEnabled &&
          _pushSupported) {
        await _enableNotifications();
      }
    } catch (e) {
      if (!mounted) return;
      setState(() => _prefs = previous);
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(_friendlyError(e))));
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.settingsPushNotifications)),
      body: CenteredContent(
          child: _isLoading
              ? const Center(
                  child: CircularProgressIndicator(color: AppTheme.accent))
              : _error != null && _prefs == null
                  ? Center(
                      child: Padding(
                        padding: const EdgeInsets.all(24),
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Text(_error!,
                                style: const TextStyle(color: AppTheme.error),
                                textAlign: TextAlign.center),
                            const SizedBox(height: 12),
                            ElevatedButton(
                                onPressed: _load,
                                child: Text(context.l10n.settingsRetry)),
                          ],
                        ),
                      ),
                    )
                  : _buildBody()),
    );
  }

  Widget _buildBody() {
    final prefs = _prefs!;
    final auth = ref.watch(authProvider).valueOrNull;
    final isAdmin = auth?.user?.isAdmin ?? false;
    // Same gate as the Books tab: a user without Chaptarr access is never in
    // a new-book audience (the server scopes recipients by instance grant),
    // so don't show them a toggle that can't do anything. Music mirrors it.
    final showBooks = auth?.connection?.services.chaptarr ?? false;
    final showMusic = auth?.connection?.services.lidarr ?? false;

    return ListView(
      // Build every child while a settings-search highlight needs to find
      // its anchor (see SettingsHighlight).
      cacheExtent: SettingsHighlight.cacheExtentFor(widget.highlightId),
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        Padding(
          padding: EdgeInsets.fromLTRB(16, 8, 16, 12),
          child: Text(
            context.l10n.notificationPreferencesHelp,
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        if (_saving) const LinearProgressIndicator(),
        SettingsHighlight(
            anchorId: SettingsAnchors.notificationsEnabled,
            highlightId: widget.highlightId,
            child: SwitchListTile(
              title: Text(context.l10n.notificationReceivePush),
              subtitle: Text(context.l10n.notificationAllDevices),
              value: prefs.pushEnabled,
              onChanged: _saving || !prefs.supportsControls
                  ? null
                  : (value) => _save(prefs.copyWith(pushEnabled: value), prefs),
            )),
        if (!prefs.supportsControls)
          Padding(
              padding: EdgeInsets.all(16),
              child: Text(context.l10n.notificationUpdateServerControls)),
        if (!prefs.serverEnabled)
          Padding(
              padding: EdgeInsets.all(16),
              child: Text(context.l10n.notificationServerDisabledSaved)),
        if (prefs.serverEnabled && !prefs.pushEnabled)
          Padding(
              padding: EdgeInsets.all(16),
              child: Text(context.l10n.notificationPersonalDisabledSaved)),
        if (isAdmin && prefs.supportsControls)
          ListTile(
            leading: const Icon(Icons.dns_outlined),
            title: Text(context.l10n.notificationServerSettings),
            subtitle: Text(context.l10n.notificationServerSettingsHelp),
            trailing: const Icon(Icons.chevron_right),
            onTap: _saving
                ? null
                : () async {
                    await context.push('/settings/push-notifications/server');
                    if (mounted) await _load();
                  },
          ),
        _SectionHeader(title: context.l10n.notificationMyNotifications),
        for (final category in pushCategories.where((c) => !c.admin))
          if ((category.service != 'chaptarr' || showBooks) &&
              (category.service != 'lidarr' || showMusic))
            _categoryToggle(category, prefs),
        if (isAdmin) ...[
          _SectionHeader(title: context.l10n.notificationAdminAlerts),
          for (final category in pushCategories.where((c) => c.admin))
            _categoryToggle(category, prefs),
        ],
        if (_pushSupported) ..._buildStatusSection(),
        const SizedBox(height: 32),
      ],
    );
  }

  /// The device status block below the category toggles: current
  /// permission state plus the relevant affordance (enable / open Settings)
  /// and a "Send test notification" button.
  List<Widget> _buildStatusSection() {
    final canReceive = _prefs!.pushEnabled && _prefs!.serverEnabled;
    final authorized =
        _authStatus == 'authorized' || _authStatus == 'provisional';
    final denied = _authStatus == 'denied';
    final notDetermined =
        _authStatus == 'notDetermined' || _authStatus == 'ephemeral';

    return [
      _SectionHeader(title: context.l10n.notificationStatus),
      _statusRow(),
      if (denied)
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                context.l10n.notificationPermissionHelp,
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              ),
              const SizedBox(height: 12),
              OutlinedButton.icon(
                onPressed: () =>
                    ref.read(pushServiceProvider).openSystemSettings(),
                icon: const Icon(Icons.settings_outlined, size: 18),
                label: Text(context.l10n.notificationOpenSystemSettings),
              ),
            ],
          ),
        ),
      if (notDetermined)
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
          child: Align(
            alignment: Alignment.centerLeft,
            child: ElevatedButton.icon(
              onPressed: canReceive && !_saving ? _enableNotifications : null,
              icon: const Icon(Icons.notifications_active_outlined, size: 18),
              label: Text(context.l10n.notificationAllowOnDevice),
            ),
          ),
        ),
      Padding(
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
        child: Align(
          alignment: Alignment.centerLeft,
          child: OutlinedButton.icon(
            onPressed: (authorized && canReceive && !_saving && !_sendingTest)
                ? _sendTest
                : null,
            icon: _sendingTest
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: AppTheme.accent),
                  )
                : const Icon(Icons.send_outlined, size: 18),
            label: Text(context.l10n.notificationSendTest),
          ),
        ),
      ),
      const SizedBox(height: 8),
    ];
  }

  /// A single row summarising the current permission state with an icon.
  Widget _statusRow() {
    final IconData icon;
    final Color color;
    final String label;
    switch (_authStatus) {
      case 'authorized':
      case 'provisional':
        icon = Icons.check_circle;
        color = AppTheme.available;
        label = context.l10n.notificationGranted;
        break;
      case 'denied':
        icon = Icons.cancel;
        color = AppTheme.error;
        label = context.l10n.notificationDenied;
        break;
      default:
        icon = Icons.help_outline;
        color = AppTheme.textSecondary;
        label = context.l10n.notificationNotRequested;
    }
    return ListTile(
      leading: Icon(icon, color: color),
      title: Text(context.l10n.notificationPermission,
          style: TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
      subtitle: Text(label, style: TextStyle(color: color, fontSize: 13)),
    );
  }

  Widget _categoryToggle(PushCategory category, NotificationPrefs prefs) {
    final unavailable =
        !prefs.serverEnabled || !prefs.categoryAllowed(category.key);
    final unsupported =
        category.key == 'request_auto_approved' && !prefs.supportsControls;
    final reason = unsupported
        ? context.l10n.notificationUpdateServer
        : unavailable
            ? context.l10n.notificationServerOff
            : !prefs.pushEnabled
                ? context.l10n.notificationPersonalOff
                : category.localizedSubtitle(context.l10n);
    return _toggle(
        title: category.localizedTitle(context.l10n),
        subtitle: reason,
        value: prefs.toJson()[category.key] as bool,
        onChanged: _saving || unavailable || unsupported || !prefs.pushEnabled
            ? null
            : (value) => _save(prefs.withCategory(category.key, value), prefs),
        anchor: category.anchor);
  }

  Widget _toggle({
    required String title,
    required String subtitle,
    required bool value,
    required ValueChanged<bool>? onChanged,
    String? anchor,
  }) {
    final tile = SwitchListTile(
      value: value,
      onChanged: onChanged,
      title: Text(title,
          style: const TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
      subtitle: Text(subtitle,
          style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
    );
    if (anchor == null) return tile;
    return SettingsHighlight(
      anchorId: anchor,
      highlightId: widget.highlightId,
      child: tile,
    );
  }
}

/// Small uppercase accent header, matching the settings screen sections.
class _SectionHeader extends StatelessWidget {
  final String title;
  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Text(
        title.toUpperCase(),
        style: const TextStyle(
          color: AppTheme.accent,
          fontSize: 12,
          fontWeight: FontWeight.w700,
          letterSpacing: 1.2,
        ),
      ),
    );
  }
}
