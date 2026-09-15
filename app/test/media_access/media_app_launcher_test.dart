import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_access/logic/media_app_launcher.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

const _plexUrl =
    'https://app.plex.tv/desktop/#!/server/m1/details?key=%2Flibrary%2Fmetadata%2F123';
const _embyUrl =
    'https://watch.example.com/prefix/web/index.html#!/item?id=123&serverId=s1';
const _jellyfinUrl =
    'https://watch.example.com/jellyfin/web/#/details?id=123&serverId=s1';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  for (final platform in [TargetPlatform.iOS, TargetPlatform.android]) {
    for (final mediaType in [MediaType.movie, MediaType.tv]) {
      test('$platform Plex passes the matched server and $mediaType to preplay',
          () async {
        final calls = _Launches(platform);
        expect(
          await calls.launcher.open(
              serviceType: 'plex', webUrl: _plexUrl, mediaType: mediaType),
          isTrue,
        );
        final uri = calls.nativeTitle;
        expect(uri.scheme, 'plex');
        expect(uri.host, 'preplay');
        expect(uri.path, '/');
        expect(uri.queryParameters, {
          'metadataKey': '/library/metadata/123',
          'metadataType': mediaType == MediaType.movie ? '1' : '2',
          'server': 'm1',
        });
        expect(calls.external.where((uri) => uri.scheme == 'https'), isEmpty);
      });
    }

    test('$platform Emby preserves encoded IDs and server selection', () async {
      final calls = _Launches(platform);
      expect(
        await calls.launcher.open(
          serviceType: 'emby',
          webUrl: 'https://watch.example.com/prefix/web/index.html'
              '#!/item?id=item%2Fone%26two&serverId=server%2Btwo',
          mediaType: MediaType.tv,
        ),
        isTrue,
      );
      final uri = calls.nativeTitle;
      expect(uri.scheme, 'emby');
      expect(uri.host, 'items');
      if (platform == TargetPlatform.android) {
        expect(uri.pathSegments, ['server+two', 'item/one&two']);
        expect(uri.toString(), contains('item%2Fone&two'));
      } else {
        expect(uri.queryParameters, {
          'serverId': 'server+two',
          'itemId': 'item/one&two',
        });
      }
      expect(calls.external.where((uri) => uri.scheme == 'https'), isEmpty);
    });

    test('$platform Jellyfin opens the app without inventing a title link',
        () async {
      final calls = _Launches(platform);
      expect(
        await calls.launcher.open(
            serviceType: 'jellyfin',
            webUrl: _jellyfinUrl,
            mediaType: MediaType.movie),
        isTrue,
      );
      if (platform == TargetPlatform.iOS) {
        expect(calls.external.single.toString(), 'org.jellyfin.expo://');
      } else {
        expect(calls.android.single, (serviceType: 'jellyfin', titleUri: null));
        expect(calls.external, isEmpty);
      }
    });

    test('$platform generic Plex shortcuts never acquire a title hint',
        () async {
      final calls = _Launches(platform);
      expect(await calls.launcher.open(serviceType: 'plex', webUrl: _plexUrl),
          isTrue);
      if (platform == TargetPlatform.iOS) {
        expect(calls.external.single.toString(), 'plex://');
      } else {
        expect(calls.android.single, (serviceType: 'plex', titleUri: null));
      }
    });
  }

  for (final (serviceType, url) in [
    ('plex', _plexUrl.replaceFirst('app.plex.tv', 'other.example.com')),
    ('plex', _plexUrl.replaceFirst('/desktop/', '/auth/')),
    ('plex', _plexUrl.replaceFirst('/server/m1/', '/server//')),
    ('plex', _plexUrl.replaceFirst('key=', 'other=')),
    ('plex', _plexUrl.replaceFirst('metadata%2F123', 'metadata%2F')),
    (
      'plex',
      _plexUrl.replaceFirst('key=%2Flibrary%2Fmetadata%2F123', 'key=%FF')
    ),
    ('emby', _embyUrl.replaceFirst('id=123', 'id=')),
    ('emby', _embyUrl.replaceFirst('&serverId=s1', '')),
    ('emby', _embyUrl.replaceFirst('/web/index.html', '/other.html')),
  ]) {
    test('an unrecognized $serviceType title URL opens only the app: $url',
        () async {
      final calls = _Launches(TargetPlatform.iOS);
      expect(
          await calls.launcher.open(
              serviceType: serviceType,
              webUrl: url,
              mediaType: MediaType.movie),
          isTrue);
      expect(calls.external.single.toString(), '$serviceType://');
    });
  }

  test('iOS tries app home when the title intent is rejected', () async {
    final calls = _Launches(TargetPlatform.iOS, externalResults: [false, true]);
    expect(
        await calls.launcher.open(
            serviceType: 'emby', webUrl: _embyUrl, mediaType: MediaType.movie),
        isTrue);
    expect(calls.external.map((uri) => uri.toString()), [
      'emby://items?serverId=s1&itemId=123',
      'emby://',
    ]);
  });

  for (final first in [false, PlatformException(code: 'launch_failed')]) {
    test('iOS falls back once to the exact web URL after native failure $first',
        () async {
      final calls =
          _Launches(TargetPlatform.iOS, externalResults: [first, false, true]);
      expect(
          await calls.launcher.open(
              serviceType: 'emby',
              webUrl: _embyUrl,
              mediaType: MediaType.movie),
          isTrue);
      expect(calls.external.map((uri) => uri.toString()), [
        'emby://items?serverId=s1&itemId=123',
        'emby://',
        _embyUrl,
      ]);
    });
  }

  test('failure to open either the app or the browser is reported', () async {
    final calls = _Launches(TargetPlatform.iOS,
        externalResults: [false, false, PlatformException(code: 'no_browser')]);
    expect(
        await calls.launcher.open(
            serviceType: 'plex', webUrl: _plexUrl, mediaType: MediaType.movie),
        isFalse);
    expect(calls.external, hasLength(3));
  });

  for (final nativeResult in [false, MissingPluginException()]) {
    test('Android falls back to the original web URL after $nativeResult',
        () async {
      final calls =
          _Launches(TargetPlatform.android, androidResults: [nativeResult]);
      expect(
          await calls.launcher.open(
              serviceType: 'emby',
              webUrl: _embyUrl,
              mediaType: MediaType.movie),
          isTrue);
      expect(calls.android.single.serviceType, 'emby');
      expect(calls.android.single.titleUri.toString(), 'emby://items/s1/123');
      expect(calls.external.single.toString(), _embyUrl);
    });
  }

  test('a generic shortcut retains the configured sign-in address on fallback',
      () async {
    final calls = _Launches(TargetPlatform.iOS, externalResults: [false, true]);
    const address = 'https://watch.example.com/custom/sign-in';
    expect(await calls.launcher.open(serviceType: 'plex', webUrl: address),
        isTrue);
    expect(calls.external.map((uri) => uri.toString()), ['plex://', address]);
  });

  for (final (platform, isWeb) in [
    (TargetPlatform.iOS, true),
    (TargetPlatform.android, true),
    (TargetPlatform.linux, false),
    (TargetPlatform.macOS, false),
    (TargetPlatform.windows, false),
  ]) {
    test('$platform web=$isWeb keeps the existing web handoff', () async {
      final calls = _Launches(platform, isWeb: isWeb);
      expect(
          await calls.launcher.open(
              serviceType: 'plex',
              webUrl: _plexUrl,
              mediaType: MediaType.movie),
          isTrue);
      expect(calls.android, isEmpty);
      expect(calls.external.single.toString(), _plexUrl);
    });
  }

  test('unknown services use only the original web URL', () async {
    final calls = _Launches(TargetPlatform.android);
    expect(await calls.launcher.open(serviceType: 'other', webUrl: _embyUrl),
        isTrue);
    expect(calls.android, isEmpty);
    expect(calls.external.single.toString(), _embyUrl);
  });

  test('an invalid web address still allows opening an installed app',
      () async {
    final calls = _Launches(TargetPlatform.iOS);
    expect(await calls.launcher.open(serviceType: 'plex', webUrl: ''), isTrue);
    expect(calls.external.single.toString(), 'plex://');
  });

  for (final url in ['', 'relative/path', 'javascript:alert(1)', 'https://']) {
    test('invalid web fallback is never launched: $url', () async {
      final calls = _Launches(TargetPlatform.android, androidResults: [false]);
      expect(
          await calls.launcher.open(serviceType: 'plex', webUrl: url), isFalse);
      expect(calls.external, isEmpty);
    });
  }

  test('Android channel carries only the service and optional native URL',
      () async {
    const channel = MethodChannel('codes.julian.cantinarr/media_apps');
    final calls = <MethodCall>[];
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      return true;
    });
    addTearDown(() => messenger.setMockMethodCallHandler(channel, null));
    final launcher = MediaAppLauncher(platform: TargetPlatform.android);
    expect(
        await launcher.open(
            serviceType: 'emby', webUrl: _embyUrl, mediaType: MediaType.movie),
        isTrue);
    expect(await launcher.open(serviceType: 'jellyfin', webUrl: _jellyfinUrl),
        isTrue);
    expect(calls.map((call) => call.method), ['open', 'open']);
    expect(calls.map((call) => call.arguments), [
      {'serviceType': 'emby', 'url': 'emby://items/s1/123'},
      {'serviceType': 'jellyfin'},
    ]);
  });
}

class _Launches {
  _Launches(
    this.platform, {
    bool isWeb = false,
    List<Object> externalResults = const [true],
    List<Object> androidResults = const [true],
  }) {
    final externalReplies = externalResults.toList();
    final androidReplies = androidResults.toList();
    launcher = MediaAppLauncher(
      platform: platform,
      isWeb: isWeb,
      launchExternal: (uri) async {
        external.add(uri);
        return _next(externalReplies);
      },
      launchAndroid: (serviceType, titleUri) async {
        android.add((serviceType: serviceType, titleUri: titleUri));
        return _next(androidReplies);
      },
    );
  }

  final TargetPlatform platform;
  late final MediaAppLauncher launcher;
  final external = <Uri>[];
  final android = <({String serviceType, Uri? titleUri})>[];

  Uri get nativeTitle => platform == TargetPlatform.iOS
      ? external.single
      : android.single.titleUri!;

  static bool _next(List<Object> replies) {
    final reply = replies.isEmpty ? false : replies.removeAt(0);
    if (reply is bool) return reply;
    throw reply;
  }
}
