import 'package:flutter/foundation.dart';

enum VideoApp {
  service('service'),
  infuse('infuse'),
  browser('browser');

  const VideoApp(this.id);
  final String id;

  static VideoApp fromId(String id) => values.firstWhere(
        (app) => app.id == id,
        orElse: () => service,
      );
}

/// Personal empty values inherit the instance default. Older servers and
/// unsupported platforms keep the service's existing app/browser behavior.
class VideoApps {
  const VideoApps({this.ios = ''});
  final String ios;

  static const serviceTypes = ['plex', 'jellyfin', 'emby'];

  factory VideoApps.fromJson(dynamic json) => VideoApps(
        ios: json is Map && json['ios'] is String ? json['ios'] as String : '',
      );

  Map<String, String> toJson() => {'ios': ios};

  VideoApp forPlatform(TargetPlatform platform, {required bool isWeb}) =>
      !isWeb && platform == TargetPlatform.iOS
          ? VideoApp.fromId(ios)
          : VideoApp.service;
}
