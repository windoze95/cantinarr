// Visual QA fixture for the real four library screens. Never shipped.
// flutter build web --release --no-pub -t test/preview/library_grid_main.dart
// Select a module with ?module=radarr (or sonarr, chaptarr, lidarr).
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/app_ambient_background.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_home_screen.dart';
import 'package:cantinarr/features/lidarr/ui/lidarr_home_screen.dart';
import 'package:cantinarr/features/radarr/ui/radarr_home_screen.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_home_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../library/library_fixture.dart';
import 'screenshot_data.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  WidgetsBinding.instance.ensureSemantics();
  final module = Uri.base.queryParameters['module'] ?? 'radarr';
  final adapter = LibraryFixtureAdapter();
  if (module == 'radarr' || module == 'sonarr') {
    adapter.records = (screenshotBodyFor(
        '/api/instances/$module-one/api/v3/${module == 'radarr' ? 'movie' : 'series'}',
        {}) as List).cast<Map<String, dynamic>>();
  } else if (module == 'chaptarr') {
    final page = screenshotBodyFor('/api/requests/book-authors', {}) as Map;
    final authors = page['authors'] as List;
    adapter.records = [for (var i = 0; i < authors.length; i++) {
      ...libraryRecord(i + 1, name: authors[i]['name'] as String),
      'images': [{'coverType': 'poster', 'remoteUrl': authors[i]['image']}],
    }];
  } else {
    adapter.records = [for (var i = 0; i < 12; i++) {
      ...libraryRecord(i + 1, name: [
        'Evening ensemble', 'Long distance radio', 'The night session',
        'Coastal quartet', 'Golden hour', 'Northern lights',
      ][i % 6]),
      if (i % 4 != 0) 'images': [
        {'coverType': 'poster', 'url': '/MediaCover/artist/${i % 3}.jpg'},
      ],
    }];
  }
  if (Uri.base.queryParameters['scroll'] == '1') {
    final originals = adapter.records;
    adapter.records = [for (var i = 0; i < 60; i++) {
      ...originals[i % originals.length], 'id': i + 1,
    }];
  }
  runApp(ProviderScope(overrides: [
    authProvider.overrideWith(() => LibraryFixtureAuth(serverUrl: Uri.base.origin)),
    backendClientProvider.overrideWithValue(
        Dio(BaseOptions(baseUrl: Uri.base.origin))..httpClientAdapter = adapter),
  ], child: MaterialApp(
    theme: AppTheme.dark,
    debugShowCheckedModeBanner: false,
    builder: (context, child) => AppAmbientBackground(child: child!),
    home: Scaffold(body: SafeArea(child: switch (module) {
      'radarr' => const RadarrHomeScreen(),
      'sonarr' => const SonarrHomeScreen(),
      'chaptarr' => const ChaptarrHomeScreen(),
      _ => const LidarrHomeScreen(),
    })),
  )));
}
