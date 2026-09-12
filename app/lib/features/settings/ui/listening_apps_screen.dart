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
      appBar: AppBar(title: const Text('Listening apps')),
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
              label: const Text("Couldn't load listening apps · Retry"),
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
        const SnackBar(
            content: Text("Couldn't save your listening apps. Try again.")),
      );
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) => ListView(
        padding: const EdgeInsets.all(16),
        children: [
          const Text('Choose which app Cantinarr opens for audiobooks. '
              'Your choices follow your account on this Cantinarr server.'),
          const SizedBox(height: 8),
          const Text('Use admin default follows each shared Audiobookshelf '
              'server’s settings. Choose an app to override that default.'),
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
          const Text('Install your chosen app and sign in to Audiobookshelf '
              'there first. ShelfPlayer opens a search for a verified book; '
              'TheShelf and the Audiobookshelf app open their home screen. '
              'Playback does not start automatically.'),
          const SizedBox(height: 12),
          const Text('If the app cannot open, Cantinarr opens Audiobookshelf '
              'in your browser. Web and desktop always use the browser.'),
        ],
      );
}
