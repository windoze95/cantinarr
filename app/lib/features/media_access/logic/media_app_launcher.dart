import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../discover/data/tmdb_models.dart';
import '../data/listening_apps.dart';
import '../data/video_apps.dart';

typedef MediaExternalLauncher = Future<bool> Function(Uri uri);
typedef MediaAndroidLauncher = Future<bool> Function(
    String serviceType, Uri? titleUri);

final mediaAppLauncherProvider = Provider((_) => MediaAppLauncher());

/// Hands off to an installed media app on phones and tablets, then to the
/// original web address if no app could open. A successful handoff does not
/// prove that the receiving app navigated to the requested title.
class MediaAppLauncher {
  MediaAppLauncher({
    TargetPlatform? platform,
    bool isWeb = kIsWeb,
    MediaExternalLauncher? launchExternal,
    MediaAndroidLauncher? launchAndroid,
  })  : _platform = platform ?? defaultTargetPlatform,
        _isWeb = isWeb,
        _launchExternal = launchExternal ?? _external,
        _launchAndroid = launchAndroid ?? _android;

  final TargetPlatform _platform;
  final bool _isWeb;
  final MediaExternalLauncher _launchExternal;
  final MediaAndroidLauncher _launchAndroid;

  ListeningApp listeningAppFor(ListeningApps apps) =>
      apps.forPlatform(_platform, isWeb: _isWeb);

  VideoApp videoAppFor(VideoApps apps) =>
      apps.forPlatform(_platform, isWeb: _isWeb);

  /// The web URL remains the verified item page (or the generic server home).
  /// ShelfPlayer's item IDs include a private, app-local connection ID, so its
  /// documented search action is used instead of inventing an exact deep link.
  /// https://github.com/rasmuslos/ShelfPlayer/issues/313
  Future<bool> openAudiobook({
    required String webUrl,
    required ListeningApps apps,
    String? title,
  }) async {
    final uri = Uri.tryParse(webUrl);
    if (uri == null ||
        !{'http', 'https'}.contains(uri.scheme) ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty) {
      return false;
    }
    final app = listeningAppFor(apps);
    if (app != ListeningApp.browser) {
      if (_platform == TargetPlatform.iOS) {
        if (app == ListeningApp.shelfplayer && title?.trim().isNotEmpty == true) {
          final search = Uri(
            scheme: 'shelfplayer',
            host: 'search',
            // Native URLComponents treats '+' literally; encode spaces as %20.
            query: 'q=${Uri.encodeComponent(title!.trim())}',
          );
          if (await _attempt(() => _launchExternal(search))) return true;
        }
        final home = Uri.parse('${app.id}://');
        if (await _attempt(() => _launchExternal(home))) return true;
      } else if (_platform == TargetPlatform.android) {
        // These clients can always be opened through their launcher activity.
        // Do not pass an ABS item ID to an undocumented title handler.
        if (await _attempt(() => _launchAndroid(app.id, null))) return true;
      }
    }
    return _attempt(() => _launchExternal(uri));
  }

  /// Supply [mediaType] only for a confirmed title link. Generic shortcuts
  /// (including unverified Plex matches and guide addresses) open the app home.
  Future<bool> open({
    required String serviceType,
    required String webUrl,
    MediaType? mediaType,
    int? tmdbId,
    VideoApps apps = const VideoApps(),
  }) async {
    final parsed = Uri.tryParse(webUrl);
    final webUri = parsed != null &&
            (parsed.scheme == 'http' || parsed.scheme == 'https') &&
            parsed.host.isNotEmpty
        ? parsed
        : null;
    final chosen = videoAppFor(apps);
    if (VideoApps.serviceTypes.contains(serviceType) &&
        chosen != VideoApp.service) {
      if (webUri == null || webUri.userInfo.isNotEmpty) return false;
      if (chosen == VideoApp.infuse) {
        // Firecore's documented links identify a title across Infuse's own
        // libraries, not the matching server/copy. Never append ?play.
        // https://support.firecore.com/hc/en-us/articles/215090997-API-for-Third-Party-Apps-Services
        final target = tmdbId != null && tmdbId > 0 &&
                (mediaType == MediaType.movie || mediaType == MediaType.tv)
            ? Uri.parse('infuse://${mediaType == MediaType.movie ? 'movie' : 'series'}/$tmdbId')
            : Uri.parse('infuse://');
        if (await _attempt(() => _launchExternal(target))) return true;
      }
      return _attempt(() => _launchExternal(webUri));
    }
    final homeUri = switch (serviceType) {
      'plex' => Uri.parse('plex://'),
      'emby' => Uri.parse('emby://'),
      // The official iOS app registers its bundle ID, not jellyfin://.
      // https://github.com/jellyfin/jellyfin-ios/blob/master/ios/Jellyfin/Info.plist
      'jellyfin' => Uri.parse('org.jellyfin.expo://'),
      _ => null,
    };

    if (!_isWeb && homeUri != null) {
      final titleUri = webUri != null && mediaType != null
          ? _titleUri(serviceType, webUri, mediaType)
          : null;
      if (_platform == TargetPlatform.iOS) {
        if (titleUri != null &&
            await _attempt(() => _launchExternal(titleUri))) {
          return true;
        }
        if (await _attempt(() => _launchExternal(homeUri))) return true;
      } else if (_platform == TargetPlatform.android) {
        // Android targets the official package and falls back to its launch
        // intent. This also opens Jellyfin, which has no VIEW URL handler.
        if (await _attempt(() => _launchAndroid(serviceType, titleUri))) {
          return true;
        }
      }
    }

    return webUri != null && await _attempt(() => _launchExternal(webUri));
  }

  Uri? _titleUri(String serviceType, Uri webUri, MediaType mediaType) {
    if (mediaType != MediaType.movie && mediaType != MediaType.tv) return null;
    try {
      // These are the server's existing canonical web links. Never substitute
      // a guessed server/title or interpret a generic sign-in address as one.
      if (!webUri.fragment.startsWith('!')) return null;
      final page = Uri.parse(webUri.fragment.substring(1));
      if (serviceType == 'plex' &&
          webUri.host == 'app.plex.tv' &&
          (webUri.path == '/desktop/' || webUri.path == '/desktop') &&
          page.pathSegments.length == 3 &&
          page.pathSegments[0] == 'server' &&
          page.pathSegments[1].isNotEmpty &&
          page.pathSegments[2] == 'details') {
        final key = page.queryParameters['key'];
        if (key == null ||
            !RegExp(r'^/library/metadata/[^/?#]+$').hasMatch(key)) {
          return null;
        }
        // Legacy preplay links remain a best-effort title hint. Current Plex
        // versions may accept the link but land at Home, which is intentional
        // app-first behavior here (never start playback automatically).
        // https://gist.github.com/jonathanfinley/a50bdd1a78d7c4cdb3533f7bf3e443ef
        // https://forums.plex.tv/t/deeplinks/940191
        return Uri(
            scheme: 'plex',
            host: 'preplay',
            path: '/',
            queryParameters: {
              'metadataKey': key,
              'metadataType': mediaType == MediaType.movie ? '1' : '2',
              'server': page.pathSegments[1],
            });
      }
      if (serviceType == 'emby' &&
          webUri.path.endsWith('/web/index.html') &&
          page.path == '/item') {
        final serverId = page.queryParameters['serverId'];
        final itemId = page.queryParameters['id'];
        if (serverId == null ||
            serverId.isEmpty ||
            itemId == null ||
            itemId.isEmpty) {
          return null;
        }
        // https://emby.media/community/topic/103624-open-movie-in-emby-via-url/
        return _platform == TargetPlatform.android
            ? Uri(
                scheme: 'emby', host: 'items', pathSegments: [serverId, itemId])
            : Uri(scheme: 'emby', host: 'items', queryParameters: {
                'serverId': serverId,
                'itemId': itemId,
              });
      }
    } on FormatException {
      // An old or malformed title link can still open the app's home screen.
    }
    return null;
  }

  static Future<bool> _attempt(Future<bool> Function() launch) async {
    try {
      return await launch();
    } catch (_) {
      return false;
    }
  }

  static Future<bool> _external(Uri uri) =>
      launchUrl(uri, mode: LaunchMode.externalApplication);

  static Future<bool> _android(String serviceType, Uri? titleUri) async =>
      await const MethodChannel('codes.julian.cantinarr/media_apps')
          .invokeMethod<bool>('open', {
        'serviceType': serviceType,
        if (titleUri != null) 'url': titleUri.toString(),
      }) ??
      false;
}
