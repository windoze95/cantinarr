import 'dart:convert';

import 'package:dio/dio.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/logic/auth_provider.dart';

/// The authorization context of Movie/TV session data. Token rotation and
/// unrelated profile/config changes do not throw away a loaded catalog.
final discoverSessionProvider = Provider<String>((ref) => ref.watch(
      authProvider.select((value) {
        final auth = value.valueOrNull;
        final connection = auth?.connection;
        final user = auth?.user;
        final permissions = [...?user?.permissions]..sort();
        final instances = [
          for (final instance in connection?.instances ?? [])
            if (instance.serviceType == 'radarr' ||
                instance.serviceType == 'sonarr')
              '${instance.serviceType}:${instance.id}',
        ]..sort();
        return jsonEncode([
          connection?.serverUrl,
          user?.id,
          user?.role,
          permissions,
          user?.child,
          user?.contentLimits?.toJson(),
          instances,
          connection?.defaultRadarrInstance?.id,
          connection?.defaultSonarrInstance?.id,
          connection?.hiddenDiscoverTabs,
          connection?.adminCatalogBrowsing,
        ]);
      }),
    ));

/// A denied read must not leave titles from an older authorization visible.
/// Network/5xx failures retain previous data and offer a retry instead.
bool discoverAccessDenied(Object? error) => error is DioException &&
    const [401, 403, 404].contains(error.response?.statusCode);

/// Scroll offsets live for the same session as the rows, even if a router
/// branch is unmounted and built again. Access changes discard the bucket.
final discoverPageStorageProvider = Provider<PageStorageBucket>((ref) {
  ref.watch(discoverSessionProvider);
  return PageStorageBucket();
});
