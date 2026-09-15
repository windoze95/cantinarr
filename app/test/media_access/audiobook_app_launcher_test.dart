import 'package:cantinarr/features/media_access/data/listening_apps.dart';
import 'package:cantinarr/features/media_access/logic/media_app_launcher.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

const _web = 'https://books.example/audiobookshelf/item/copy-1';
const _apps = ListeningApps(ios: 'shelfplayer', android: 'theshelf');

void main() {
  test('ShelfPlayer searches a verified title without guessing its local ID',
      () async {
    final urls = <Uri>[];
    final launcher = MediaAppLauncher(
      platform: TargetPlatform.iOS,
      isWeb: false,
      launchExternal: (uri) async {
        urls.add(uri);
        return true;
      },
    );
    expect(
        await launcher.openAudiobook(
            webUrl: _web, apps: _apps, title: 'A & B / 100%'),
        isTrue);
    expect(urls.single.scheme, 'shelfplayer');
    expect(urls.single.host, 'search');
    expect(urls.single.queryParameters, {'q': 'A & B / 100%'});
    expect(urls.single.toString(),
        'shelfplayer://search?q=A%20%26%20B%20%2F%20100%25');
    expect(urls.single.toString(), isNot(contains('copy-1')));
  });

  test('generic ShelfPlayer shortcuts open home without a search or playback',
      () async {
    final urls = <Uri>[];
    final launcher = MediaAppLauncher(
        platform: TargetPlatform.iOS,
        isWeb: false,
        launchExternal: (uri) async {
          urls.add(uri);
          return true;
        });
    expect(await launcher.openAudiobook(webUrl: _web, apps: _apps), isTrue);
    expect(urls.single.toString(), 'shelfplayer://');
  });

  for (final failure in [false, PlatformException(code: 'missing_app')]) {
    test(
        'ShelfPlayer falls back through home to the exact browser link: $failure',
        () async {
      final urls = <Uri>[];
      final launcher = MediaAppLauncher(
          platform: TargetPlatform.iOS,
          isWeb: false,
          launchExternal: (uri) async {
            urls.add(uri);
            if (uri.scheme == 'https') return true;
            if (failure is Exception) throw failure;
            return false;
          });
      expect(
          await launcher.openAudiobook(
              webUrl: _web, apps: _apps, title: 'Book'),
          isTrue);
      expect(urls.map((uri) => uri.toString()),
          ['shelfplayer://search?q=Book', 'shelfplayer://', _web]);
    });
  }

  for (final app in ['theshelf', 'audiobookshelf']) {
    test(
        'Android opens the chosen $app package without an invented title intent',
        () async {
      final calls = <String>[];
      final launcher = MediaAppLauncher(
          platform: TargetPlatform.android,
          isWeb: false,
          launchAndroid: (id, uri) async {
            expect(uri, isNull);
            calls.add(id);
            return true;
          },
          launchExternal: (_) async {
            fail('Unexpected browser launch');
          });
      expect(
          await launcher.openAudiobook(
              webUrl: _web, apps: ListeningApps(android: app), title: 'Book'),
          isTrue);
      expect(calls, [app]);
    });
  }

  test('missing Android app quietly opens the verified browser URL', () async {
    final urls = <Uri>[];
    final launcher = MediaAppLauncher(
        platform: TargetPlatform.android,
        isWeb: false,
        launchAndroid: (_, __) async => false,
        launchExternal: (uri) async {
          urls.add(uri);
          return true;
        });
    expect(await launcher.openAudiobook(webUrl: _web, apps: _apps), isTrue);
    expect(urls.single.toString(), _web);
  });

  for (final (platform, isWeb, apps) in [
    (TargetPlatform.iOS, true, _apps),
    (TargetPlatform.android, true, _apps),
    (TargetPlatform.macOS, false, _apps),
    (TargetPlatform.iOS, false, const ListeningApps(ios: 'browser')),
    (TargetPlatform.iOS, false, const ListeningApps(ios: 'theshelf')),
    (TargetPlatform.android, false, const ListeningApps(android: 'unknown')),
  ]) {
    test(
        '$platform web=$isWeb safely resolves to the browser: ${apps.toJson()}',
        () async {
      final urls = <Uri>[];
      final launcher = MediaAppLauncher(
          platform: platform,
          isWeb: isWeb,
          launchAndroid: (_, __) async {
            fail('Unexpected Android launch');
          },
          launchExternal: (uri) async {
            urls.add(uri);
            return true;
          });
      expect(await launcher.openAudiobook(webUrl: _web, apps: apps), isTrue);
      expect(urls.single.toString(), _web);
      expect(launcher.listeningAppFor(apps), ListeningApp.browser);
    });
  }

  test('unsafe browser addresses never get handed to another app', () async {
    final launcher = MediaAppLauncher(
        platform: TargetPlatform.iOS,
        isWeb: false,
        launchExternal: (_) async {
          fail('Unsafe address was used');
        });
    for (final url in [
      'javascript:alert(1)',
      'https://secret:password@books.example/',
      'https:///'
    ]) {
      expect(await launcher.openAudiobook(webUrl: url, apps: _apps), isFalse);
    }
  });
}
