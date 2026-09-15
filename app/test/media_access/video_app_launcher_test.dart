import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_access/data/video_apps.dart';
import 'package:cantinarr/features/media_access/logic/media_app_launcher.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

const _web = 'https://watch.example.com/base/web/#/details?id=copy-1';
const _infuse = VideoApps(ios: 'infuse');

void main() {
  for (final service in VideoApps.serviceTypes) {
    for (final media in [MediaType.movie, MediaType.tv]) {
      test('$service opens the $media in Infuse without starting playback', () async {
        final urls = <Uri>[];
        final launcher = MediaAppLauncher(platform: TargetPlatform.iOS,
          isWeb: false, launchExternal: (uri) async { urls.add(uri); return true; });
        expect(await launcher.open(serviceType: service, webUrl: _web,
          apps: _infuse, mediaType: media, tmdbId: 10378), isTrue);
        expect(urls.single.toString(),
          'infuse://${media == MediaType.movie ? 'movie' : 'series'}/10378');
        expect(urls.single.query, isEmpty);
      });
    }
    test('$service generic shortcuts carry no title hint', () async {
      final urls = <Uri>[];
      final launcher = MediaAppLauncher(platform: TargetPlatform.iOS,
        isWeb: false, launchExternal: (uri) async { urls.add(uri); return true; });
      expect(await launcher.open(serviceType: service, webUrl: _web,
        apps: _infuse, tmdbId: 10378), isTrue);
      expect(urls.single.toString(), 'infuse://');
    });
  }

  for (final failure in [false, PlatformException(code: 'missing_app')]) {
    test('Infuse launch failure quietly retains the exact browser URL: $failure', () async {
      final urls = <Uri>[];
      final launcher = MediaAppLauncher(platform: TargetPlatform.iOS, isWeb: false,
        launchExternal: (uri) async {
          urls.add(uri);
          if (uri.scheme == 'https') return true;
          if (failure is Exception) throw failure;
          return false;
        });
      expect(await launcher.open(serviceType: 'plex', webUrl: _web,
        apps: _infuse, mediaType: MediaType.movie, tmdbId: 10378), isTrue);
      expect(urls.map((uri) => uri.toString()), ['infuse://movie/10378', _web]);
    });
  }

  test('an explicit Browser choice skips native apps', () async {
    final urls = <Uri>[];
    final launcher = MediaAppLauncher(platform: TargetPlatform.iOS, isWeb: false,
      launchExternal: (uri) async { urls.add(uri); return true; });
    expect(await launcher.open(serviceType: 'emby', webUrl: _web,
      apps: const VideoApps(ios: 'browser'), mediaType: MediaType.tv, tmdbId: 1), isTrue);
    expect(urls.single.toString(), _web);
  });

  for (final id in [null, 0, -1]) {
    test('invalid or missing TMDB id opens Infuse home: $id', () async {
      final urls = <Uri>[];
      final launcher = MediaAppLauncher(platform: TargetPlatform.iOS, isWeb: false,
        launchExternal: (uri) async { urls.add(uri); return true; });
      await launcher.open(serviceType: 'plex', webUrl: _web, apps: _infuse,
        mediaType: MediaType.movie, tmdbId: id);
      expect(urls.single.toString(), 'infuse://');
    });
  }

  for (final platform in TargetPlatform.values) {
    test('$platform web always uses the browser', () async {
      final urls = <Uri>[];
      final launcher = MediaAppLauncher(platform: platform, isWeb: true,
        launchExternal: (uri) async { urls.add(uri); return true; });
      await launcher.open(serviceType: 'plex', webUrl: _web, apps: _infuse,
        mediaType: MediaType.movie, tmdbId: 1);
      expect(urls.single.toString(), _web);
    });
    if (platform == TargetPlatform.iOS) continue;
    test('$platform ignores an iOS Infuse choice', () async {
      final urls = <Uri>[];
      final packages = <String>[];
      final launcher = MediaAppLauncher(platform: platform, isWeb: false,
        launchExternal: (uri) async { urls.add(uri); return true; },
        launchAndroid: (service, uri) async { packages.add(service); return true; });
      await launcher.open(serviceType: 'plex', webUrl: _web, apps: _infuse);
      if (platform == TargetPlatform.android) {
        expect(packages, ['plex']);
        expect(urls, isEmpty);
      } else {
        expect(urls.single.toString(), _web);
      }
    });
  }

  test('old or unknown preference values retain the official app', () {
    for (final raw in [null, {}, {'ios': 'unknown'}]) {
      expect(VideoApps.fromJson(raw).forPlatform(TargetPlatform.iOS, isWeb: false),
        VideoApp.service);
    }
  });

  test('Infuse does not open without a valid fallback address', () async {
    final launcher = MediaAppLauncher(platform: TargetPlatform.iOS, isWeb: false,
      launchExternal: (_) async => fail('Invalid address launched'));
    for (final url in ['', 'javascript:alert(1)', 'https://user:pass@example.com']) {
      expect(await launcher.open(serviceType: 'plex', webUrl: url, apps: _infuse), isFalse);
    }
  });
}
