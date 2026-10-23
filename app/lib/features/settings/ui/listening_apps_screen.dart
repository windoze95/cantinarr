import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/adaptive.dart';
import '../../auth/logic/auth_provider.dart';
import '../../media_access/data/listening_apps.dart';
import '../../media_access/data/media_access_service.dart';
import '../../media_access/logic/listen_links_provider.dart';
import '../../media_access/logic/listening_apps_provider.dart';
import '../../media_access/ui/listening_app_fields.dart';

class ListeningAppsScreen extends ConsumerWidget {
  const ListeningAppsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final auth = ref.watch(authProvider).valueOrNull;
    final preferences = ref.watch(listeningAppPreferencesProvider);
    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.settingsListeningApps)),
      body: CenteredContent(
        child: preferences.when(
          skipLoadingOnReload: false,
          data: (apps) => _PreferencesForm(
            key: ValueKey((auth?.connection?.serverUrl, auth?.user?.id)),
            apps: apps,
          ),
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, __) => Center(
            child: TextButton.icon(
              onPressed: () => ref.invalidate(listeningAppPreferencesProvider),
              icon: const Icon(Icons.refresh),
              label: Text(context.l10n.settingsListeningAppsLoadError),
            ),
          ),
        ),
      ),
    );
  }
}

class _PreferencesForm extends ConsumerStatefulWidget {
  const _PreferencesForm({super.key, required this.apps});
  final ListeningApps apps;

  @override
  ConsumerState<_PreferencesForm> createState() => _PreferencesFormState();
}

class _PreferencesFormState extends ConsumerState<_PreferencesForm> {
  bool _saving = false;
  int _formVersion = 0;

  Future<void> _save(ListeningApps apps) async {
    setState(() => _saving = true);
    try {
      await ref
          .read(mediaAccessServiceProvider)
          .saveListeningAppPreferences(apps);
      if (!mounted) return;
      ref.invalidate(listeningAppPreferencesProvider);
      ref.read(mediaAccessRevisionProvider.notifier).state++;
    } catch (_) {
      if (!mounted) return;
      setState(() => _formVersion++);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(context.l10n.settingsListeningAppsSaveError)),
      );
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) => ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Text(context.l10n.settingsListeningAppsHelp),
          const SizedBox(height: 8),
          Text(context.l10n.settingsListeningAppsDefaultHelp),
          const SizedBox(height: 24),
          ListeningAppFields(
            key: ValueKey(_formVersion),
            value: widget.apps,
            inheritDefaults: true,
            onChanged: _saving ? null : _save,
          ),
          if (_saving) ...[
            const SizedBox(height: 16),
            const LinearProgressIndicator(),
          ],
          const SizedBox(height: 24),
          Text(context.l10n.settingsListeningAppsPlaybackHelp),
          const SizedBox(height: 12),
          Text(context.l10n.settingsListeningAppsFallbackHelp),
        ],
      );
}
