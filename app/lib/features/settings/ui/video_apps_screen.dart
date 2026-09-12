import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/layout/adaptive.dart';
import '../../auth/logic/auth_provider.dart';
import '../../media_access/data/media_access_service.dart';
import '../../media_access/data/video_apps.dart';
import '../../media_access/logic/video_apps_provider.dart';
import '../../media_access/ui/video_app_field.dart';

class VideoAppsScreen extends ConsumerStatefulWidget {
  const VideoAppsScreen({super.key});

  @override
  ConsumerState<VideoAppsScreen> createState() => _VideoAppsScreenState();
}

class _VideoAppsScreenState extends ConsumerState<VideoAppsScreen>
    with WidgetsBindingObserver {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      ref.invalidate(videoAppPreferencesProvider);
    }
  }

  @override
  Widget build(BuildContext context) {
    final auth = ref.watch(authProvider).valueOrNull;
    final available = auth?.connection?.mediaServerInstances
        .map((server) => server.serviceType).toSet() ?? const <String>{};
    final services = VideoApps.serviceTypes.where(available.contains).toList();
    final preferences = ref.watch(videoAppPreferencesProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('Video apps')),
      body: CenteredContent(
        child: preferences.when(
          skipLoadingOnReload: false,
          data: (apps) => _PreferencesForm(
            key: ValueKey((auth?.connection?.serverUrl, auth?.user?.id)),
            apps: apps,
            services: services,
            refreshing: preferences.isLoading,
          ),
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, __) => Center(
            child: TextButton.icon(
              onPressed: () => ref.invalidate(videoAppPreferencesProvider),
              icon: const Icon(Icons.refresh),
              label: const Text("Couldn't load video apps · Retry"),
            ),
          ),
        ),
      ),
    );
  }
}

class _PreferencesForm extends ConsumerStatefulWidget {
  const _PreferencesForm({super.key, required this.apps, required this.services,
    required this.refreshing});
  final Map<String, VideoApps> apps;
  final List<String> services;
  final bool refreshing;

  @override
  ConsumerState<_PreferencesForm> createState() => _PreferencesFormState();
}

class _PreferencesFormState extends ConsumerState<_PreferencesForm> {
  bool _saving = false;
  int _formVersion = 0;

  Future<void> _save(String serviceType, VideoApps apps) async {
    if (_saving || widget.refreshing) return;
    setState(() => _saving = true);
    try {
      await ref.read(mediaAccessServiceProvider).saveVideoAppPreferences({
        ...widget.apps,
        serviceType: apps,
      });
      if (!mounted) return;
      ref.invalidate(videoAppPreferencesProvider);
      ref.read(videoAppRevisionProvider.notifier).state++;
    } catch (_) {
      if (!mounted) return;
      setState(() => _formVersion++);
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
        content: Text("Couldn't save your video apps. Try again."),
      ));
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) => ListView(
    padding: const EdgeInsets.all(16),
    children: [
      const Text('Choose which app Cantinarr opens for movies and shows on '
          'iPhone and iPad. Your choices follow your account on this Cantinarr server.'),
      const SizedBox(height: 8),
      const Text('Use admin default follows each shared server’s settings. '
          'Your choice overrides every server of that service type.'),
      if (widget.services.isEmpty) ...[
        const SizedBox(height: 24),
        const Text('No video servers are configured for your account.'),
      ],
      for (final service in widget.services) ...[
        const SizedBox(height: 24),
        Text(mediaServerTypeLabel(service),
            style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 8),
        VideoAppField(
          key: ValueKey('$service:$_formVersion'),
          serviceType: service,
          value: widget.apps[service] ?? const VideoApps(),
          inheritDefaults: true,
          onChanged: _saving || widget.refreshing ? null : (apps) => _save(service, apps),
        ),
      ],
      if (_saving || widget.refreshing) ...[
        const SizedBox(height: 16),
        const LinearProgressIndicator(),
      ],
      const SizedBox(height: 24),
      const Text('Install Infuse and connect your media servers there first. '
          'Open in Infuse opens the movie or show across its connected libraries; '
          'it cannot select a particular server or copy. If the title is missing '
          'in Infuse, it may show a title page without a playable copy. '
          'Cantinarr does not start playback automatically.'),
      Align(
        alignment: Alignment.centerLeft,
        child: TextButton.icon(
          onPressed: () => launchUrl(Uri.parse('https://firecore.com/infuse'),
              mode: LaunchMode.externalApplication),
          icon: const Icon(Icons.open_in_new),
          label: const Text('Get Infuse'),
        ),
      ),
      const Text('If Infuse cannot open, Cantinarr opens the original server link '
          'in your browser. Android uses the Plex, Jellyfin, or Emby app. '
          'Web and desktop use the browser.'),
    ],
  );
}
