import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../notification_prefs.dart';
import '../notification_prefs_service.dart';

/// Lets the current user choose which push notifications they receive. Loads
/// the saved preferences on open and persists each toggle immediately,
/// reverting the switch if the server rejects the change.
class NotificationPreferencesScreen extends ConsumerStatefulWidget {
  const NotificationPreferencesScreen({super.key});

  @override
  ConsumerState<NotificationPreferencesScreen> createState() =>
      _NotificationPreferencesScreenState();
}

class _NotificationPreferencesScreenState
    extends ConsumerState<NotificationPreferencesScreen> {
  bool _isLoading = true;
  String? _error;
  NotificationPrefs? _prefs;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _load() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final prefs =
          await ref.read(notificationPrefsServiceProvider).getPreferences();
      if (!mounted) return;
      setState(() {
        _prefs = prefs;
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
      body: _isLoading
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
              : _buildBody(),
    );
  }

  Widget _buildBody() {
    final prefs = _prefs!;
    final isAdmin = ref.watch(authProvider).valueOrNull?.user?.isAdmin ?? false;

    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
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
        ),
        if (isAdmin)
          _toggle(
            title: 'New requests to review',
            subtitle: 'When someone submits a request needing approval',
            value: prefs.requestPending,
            onChanged: (v) => _save(prefs.copyWith(requestPending: v), prefs),
          ),
        _toggle(
          title: 'New movie available',
          subtitle: 'When a movie finishes downloading',
          value: prefs.newMovie,
          onChanged: (v) => _save(prefs.copyWith(newMovie: v), prefs),
        ),
        _toggle(
          title: 'New episode available',
          subtitle: 'When a new episode is available',
          value: prefs.newEpisode,
          onChanged: (v) => _save(prefs.copyWith(newEpisode: v), prefs),
        ),
        const SizedBox(height: 32),
      ],
    );
  }

  Widget _toggle({
    required String title,
    required String subtitle,
    required bool value,
    required ValueChanged<bool> onChanged,
  }) {
    return SwitchListTile(
      value: value,
      onChanged: onChanged,
      activeThumbColor: AppTheme.accent,
      title: Text(title,
          style: const TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
      subtitle: Text(subtitle,
          style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
    );
  }
}
