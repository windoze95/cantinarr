import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/settings_highlight.dart';
import '../../auth/logic/auth_provider.dart';
import '../../settings/settings_anchors.dart';
import '../notification_prefs.dart';
import '../notification_prefs_service.dart';
import '../push_service.dart';

/// Lets the current user choose which push notifications they receive. Loads
/// the saved preferences on open and persists each toggle immediately,
/// reverting the switch if the server rejects the change.
class NotificationPreferencesScreen extends ConsumerStatefulWidget {
  /// Settings-search anchor to scroll to and flash on arrival.
  final String? highlightId;

  const NotificationPreferencesScreen({super.key, this.highlightId});

  @override
  ConsumerState<NotificationPreferencesScreen> createState() =>
      _NotificationPreferencesScreenState();
}

class _NotificationPreferencesScreenState
    extends ConsumerState<NotificationPreferencesScreen>
    with WidgetsBindingObserver {
  bool _isLoading = true;
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
      _refreshAuthStatus();
    }
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
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
        SnackBar(content: Text(describePushTest(result))),
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
    setState(() => _prefs = updated);
    try {
      final saved = await ref
          .read(notificationPrefsServiceProvider)
          .updatePreferences(updated);
      if (!mounted) return;
      setState(() => _prefs = saved);
    } catch (e) {
      if (!mounted) return;
      setState(() => _prefs = previous);
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(_friendlyError(e))));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Notification Preferences')),
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
                                onPressed: _load, child: const Text('Retry')),
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
    // so don't show them a toggle that can't do anything.
    final showBooks = auth?.connection?.services.chaptarr ?? false;

    return ListView(
      // Build every child while a settings-search highlight needs to find
      // its anchor (see SettingsHighlight).
      cacheExtent: SettingsHighlight.cacheExtentFor(widget.highlightId),
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        if (_pushSupported) ..._buildStatusSection(),
        const Padding(
          padding: EdgeInsets.fromLTRB(16, 8, 16, 12),
          child: Text(
            'Choose which push notifications you receive on this account.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
        _toggle(
          title: 'Request approved or denied',
          subtitle: 'When your request is approved or denied',
          value: prefs.requestDecision,
          onChanged: (v) => _save(prefs.copyWith(requestDecision: v), prefs),
          anchor: SettingsAnchors.notificationsRequestDecision,
        ),
        if (isAdmin) ...[
          _toggle(
            title: 'New requests to review',
            subtitle: 'When someone submits a request needing approval',
            value: prefs.requestPending,
            onChanged: (v) => _save(prefs.copyWith(requestPending: v), prefs),
            anchor: SettingsAnchors.notificationsRequestPending,
          ),
          _toggle(
            title: 'Problem reports',
            subtitle: 'When someone reports a problem with their media',
            value: prefs.issueCreated,
            onChanged: (v) => _save(prefs.copyWith(issueCreated: v), prefs),
            anchor: SettingsAnchors.notificationsProblemReports,
          ),
          _toggle(
            title: 'Fixes awaiting approval',
            subtitle: 'When the assistant proposes a fix that needs approval',
            value: prefs.agentActionPending,
            onChanged: (v) =>
                _save(prefs.copyWith(agentActionPending: v), prefs),
            anchor: SettingsAnchors.notificationsAgentFixes,
          ),
          _toggle(
            title: 'Weekly agent summary',
            subtitle:
                'One line a week: what resolved itself, what your rules handled, what needs you',
            value: prefs.agentDigest,
            onChanged: (v) => _save(prefs.copyWith(agentDigest: v), prefs),
            anchor: SettingsAnchors.notificationsAgentDigest,
          ),
          _toggle(
            title: 'Plex access requests',
            subtitle:
                'When someone shares their Plex email and needs you to grant them Plex',
            value: prefs.plexAccessRequest,
            onChanged: (v) =>
                _save(prefs.copyWith(plexAccessRequest: v), prefs),
            anchor: SettingsAnchors.notificationsPlexAccessRequests,
          ),
          _toggle(
            title: 'Quality upgrades',
            subtitle:
                'When an existing movie, episode, or book is replaced with a better version',
            value: prefs.contentUpgraded,
            onChanged: (v) => _save(prefs.copyWith(contentUpgraded: v), prefs),
            anchor: SettingsAnchors.notificationsQualityUpgrades,
          ),
        ],
        _toggle(
          title: 'New movie available',
          subtitle: 'When a movie finishes downloading',
          value: prefs.newMovie,
          onChanged: (v) => _save(prefs.copyWith(newMovie: v), prefs),
          anchor: SettingsAnchors.notificationsNewMovie,
        ),
        _toggle(
          title: 'New episodes available',
          subtitle: 'When new episodes are available',
          value: prefs.newEpisode,
          onChanged: (v) => _save(prefs.copyWith(newEpisode: v), prefs),
          anchor: SettingsAnchors.notificationsNewEpisode,
        ),
        if (showBooks)
          _toggle(
            title: 'New book available',
            subtitle: 'When a book finishes downloading',
            value: prefs.newBook,
            onChanged: (v) => _save(prefs.copyWith(newBook: v), prefs),
            anchor: SettingsAnchors.notificationsNewBook,
          ),
        _toggle(
          title: 'Plex invite sent',
          subtitle: 'When your Plex invite goes out',
          value: prefs.plexInviteSent,
          onChanged: (v) => _save(prefs.copyWith(plexInviteSent: v), prefs),
          anchor: SettingsAnchors.notificationsPlexInviteSent,
        ),
        _toggle(
          title: 'My report updates',
          subtitle:
              'When the assistant has a question about your report, a fix is ready to confirm, or your report closes',
          value: prefs.issueReportUpdate,
          onChanged: (v) => _save(prefs.copyWith(issueReportUpdate: v), prefs),
          anchor: SettingsAnchors.notificationsReportUpdates,
        ),
        const SizedBox(height: 32),
      ],
    );
  }

  /// The push status block shown above the category toggles: current
  /// permission state plus the relevant affordance (enable / open Settings)
  /// and a "Send test notification" button.
  List<Widget> _buildStatusSection() {
    final authorized =
        _authStatus == 'authorized' || _authStatus == 'provisional';
    final denied = _authStatus == 'denied';
    final notDetermined =
        _authStatus == 'notDetermined' || _authStatus == 'ephemeral';

    return [
      const _SectionHeader(title: 'Status'),
      _statusRow(),
      if (denied)
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                'Notifications are turned off. Enable them in system settings '
                'to receive push notifications.',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              ),
              const SizedBox(height: 12),
              OutlinedButton.icon(
                onPressed: () =>
                    ref.read(pushServiceProvider).openSystemSettings(),
                icon: const Icon(Icons.settings_outlined, size: 18),
                label: const Text('Open system settings'),
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
              onPressed: _enableNotifications,
              icon: const Icon(Icons.notifications_active_outlined, size: 18),
              label: const Text('Enable notifications'),
            ),
          ),
        ),
      Padding(
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
        child: Align(
          alignment: Alignment.centerLeft,
          child: OutlinedButton.icon(
            onPressed: (authorized && !_sendingTest) ? _sendTest : null,
            icon: _sendingTest
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: AppTheme.accent),
                  )
                : const Icon(Icons.send_outlined, size: 18),
            label: const Text('Send test notification'),
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
        label = 'Granted';
        break;
      case 'denied':
        icon = Icons.cancel;
        color = AppTheme.error;
        label = 'Denied';
        break;
      default:
        icon = Icons.help_outline;
        color = AppTheme.textSecondary;
        label = 'Not yet requested';
    }
    return ListTile(
      leading: Icon(icon, color: color),
      title: const Text('Notification permission',
          style: TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
      subtitle: Text(label, style: TextStyle(color: color, fontSize: 13)),
    );
  }

  Widget _toggle({
    required String title,
    required String subtitle,
    required bool value,
    required ValueChanged<bool> onChanged,
    String? anchor,
  }) {
    final tile = SwitchListTile(
      value: value,
      onChanged: onChanged,
      activeThumbColor: AppTheme.accent,
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
