import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('ServiceInstance preserves nullable media download capability', () {
    final enabled = ServiceInstance.fromJson(const {
      'id': 'radarr-main',
      'service_type': 'radarr',
      'name': 'Movies',
      'is_default': true,
      'media_downloads': true,
    });
    final legacy = ServiceInstance.fromJson(const {
      'id': 'sonarr-main',
      'service_type': 'sonarr',
      'name': 'TV',
    });

    expect(enabled.mediaDownloads, isTrue);
    expect(enabled.toJson()['media_downloads'], isTrue);
    expect(legacy.mediaDownloads, isNull);
    expect(legacy.toJson().containsKey('media_downloads'), isFalse);
  });

  test('exact per-instance media download capability wins', () {
    const connection = BackendConnection(
      serverUrl: 'https://cantinarr.example',
      accessToken: 'access',
      refreshToken: 'refresh',
      services: AvailableServices(mediaDownloads: true),
      instances: [
        ServiceInstance(
          id: 'radarr-main',
          serviceType: 'radarr',
          name: 'Movies',
          mediaDownloads: true,
        ),
        ServiceInstance(
          id: 'radarr-4k',
          serviceType: 'radarr',
          name: '4K Movies',
          mediaDownloads: false,
        ),
      ],
    );

    expect(connection.mediaDownloadsEnabledFor('radarr-main'), isTrue);
    expect(connection.mediaDownloadsEnabledFor('radarr-4k'), isFalse);
    expect(connection.mediaDownloadsEnabledFor('missing'), isFalse);
    expect(connection.mediaDownloadsEnabledFor(null), isFalse);
  });

  test('mixed capability payload fails closed for a missing instance value', () {
    const connection = BackendConnection(
      serverUrl: 'https://cantinarr.example',
      accessToken: 'access',
      refreshToken: 'refresh',
      services: AvailableServices(mediaDownloads: true),
      instances: [
        ServiceInstance(
          id: 'radarr-main',
          serviceType: 'radarr',
          name: 'Movies',
          mediaDownloads: true,
        ),
        ServiceInstance(
          id: 'sonarr-legacy',
          serviceType: 'sonarr',
          name: 'TV',
        ),
      ],
    );

    expect(connection.mediaDownloadsEnabledFor('sonarr-legacy'), isFalse);
  });

  test('downloadInstances lists usenet clients before torrent clients', () {
    const connection = BackendConnection(
      serverUrl: 'https://cantinarr.example',
      accessToken: 'access',
      refreshToken: 'refresh',
      instances: [
        ServiceInstance(
            id: 'qbittorrent-a', serviceType: 'qbittorrent', name: 'qBit'),
        ServiceInstance(id: 'deluge-a', serviceType: 'deluge', name: 'Deluge'),
        ServiceInstance(
            id: 'qbittorrent-b',
            serviceType: 'qbittorrent',
            name: 'qBit (Yana)'),
        ServiceInstance(id: 'radarr-main', serviceType: 'radarr', name: 'Movies'),
        ServiceInstance(id: 'sabnzbd-a', serviceType: 'sabnzbd', name: 'SAB'),
        ServiceInstance(
            id: 'transmission-a', serviceType: 'transmission', name: 'Trans'),
        ServiceInstance(id: 'nzbget-a', serviceType: 'nzbget', name: 'NZBGet'),
        ServiceInstance(
            id: 'rutorrent-a', serviceType: 'rutorrent', name: 'ruTorrent'),
      ],
    );

    // Usenet group first, then torrents; server order kept within each group,
    // so Deluge stays between the two qBittorrent instances and ruTorrent,
    // last on the server, closes the torrent group.
    expect(
      connection.downloadInstances.map((i) => i.id).toList(),
      ['sabnzbd-a', 'nzbget-a', 'qbittorrent-a', 'deluge-a', 'qbittorrent-b',
          'transmission-a', 'rutorrent-a'],
    );
  });

  test('mediaServerInstances lists only media-server types', () {
    const connection = BackendConnection(
      serverUrl: 'https://cantinarr.example',
      accessToken: 'access',
      refreshToken: 'refresh',
      instances: [
        ServiceInstance(id: 'radarr-main', serviceType: 'radarr', name: 'Movies'),
        ServiceInstance(
            id: 'jf-a', serviceType: 'jellyfin', name: 'Home Jellyfin'),
        ServiceInstance(id: 'tautulli-a', serviceType: 'tautulli', name: 'T'),
        ServiceInstance(id: 'tracearr-a', serviceType: 'tracearr', name: 'TR'),
        ServiceInstance(id: 'jf-b', serviceType: 'jellyfin', name: 'Cabin'),
        ServiceInstance(id: 'em-a', serviceType: 'emby', name: 'Den Emby'),
      ],
    );

    expect(mediaServerServiceTypes, containsAll(['jellyfin', 'emby', 'plex']));
    expect(
      connection.mediaServerInstances.map((i) => i.id).toList(),
      ['jf-a', 'jf-b', 'em-a'],
    );
    // A media server is neither a library nor a download client, and a
    // watch-history provider is not a media server.
    expect(connection.radarrInstances.map((i) => i.id), ['radarr-main']);
    expect(connection.downloadInstances, isEmpty);
    expect(
      connection.watchHistoryInstances.map((i) => i.id).toList(),
      ['tautulli-a', 'tracearr-a'],
    );

    const none = BackendConnection(
      serverUrl: 'https://cantinarr.example',
      accessToken: 'access',
      refreshToken: 'refresh',
      instances: [
        ServiceInstance(id: 'radarr-main', serviceType: 'radarr', name: 'Movies'),
      ],
    );
    expect(none.mediaServerInstances, isEmpty);
  });

  test('watchHistoryInstances lists Tautulli and Tracearr in server order', () {
    const connection = BackendConnection(
      serverUrl: 'https://cantinarr.example',
      accessToken: 'access',
      refreshToken: 'refresh',
      instances: [
        ServiceInstance(id: 'radarr-main', serviceType: 'radarr', name: 'Movies'),
        ServiceInstance(id: 'tracearr-a', serviceType: 'tracearr', name: 'TR'),
        ServiceInstance(id: 'jf-a', serviceType: 'jellyfin', name: 'Jellyfin'),
        ServiceInstance(id: 'tautulli-a', serviceType: 'tautulli', name: 'T'),
      ],
    );

    expect(watchHistoryServiceTypes, {'tautulli', 'tracearr'});
    expect(
      connection.watchHistoryInstances.map((i) => i.id).toList(),
      ['tracearr-a', 'tautulli-a'],
    );
    expect(connection.mediaServerInstances.map((i) => i.id), ['jf-a']);
  });

  test('legacy payload falls back to the global capability', () {
    const connection = BackendConnection(
      serverUrl: 'https://cantinarr.example',
      accessToken: 'access',
      refreshToken: 'refresh',
      services: AvailableServices(mediaDownloads: true),
      instances: [
        ServiceInstance(
          id: 'radarr-main',
          serviceType: 'radarr',
          name: 'Movies',
        ),
      ],
    );

    expect(connection.mediaDownloadsEnabledFor('radarr-main'), isTrue);
    expect(connection.mediaDownloadsEnabledFor('missing'), isTrue);
    expect(connection.mediaDownloadsEnabledFor(null), isTrue);
  });

  test('the access guide shows for a granted server or an askable Plex', () {
    const none = BackendConnection(
      serverUrl: 'http://localhost',
      accessToken: 'a',
      refreshToken: 'r',
    );
    expect(none.mediaAccessGuideVisible, isFalse);
    expect(none.mediaAccessGuideTypes, isEmpty);

    const askable = BackendConnection(
      serverUrl: 'http://localhost',
      accessToken: 'a',
      refreshToken: 'r',
      plexAccessRequestable: true,
    );
    expect(askable.mediaAccessGuideVisible, isTrue);
    expect(askable.mediaAccessGuideTypes, {'plex'});

    const granted = BackendConnection(
      serverUrl: 'http://localhost',
      accessToken: 'a',
      refreshToken: 'r',
      instances: [
        ServiceInstance(id: 'jf-a', serviceType: 'jellyfin', name: 'Home'),
        ServiceInstance(id: 'px-a', serviceType: 'plex', name: 'Cantina'),
      ],
    );
    expect(granted.mediaAccessGuideVisible, isTrue);
    expect(granted.mediaAccessGuideTypes, {'jellyfin', 'plex'});
    expect(granted.copyWith(plexAccessRequestable: true).mediaAccessGuideTypes,
        {'jellyfin', 'plex'});
  });
}
