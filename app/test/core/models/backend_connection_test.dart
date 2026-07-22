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
}
