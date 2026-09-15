import 'package:flutter/foundation.dart';

enum ListeningApp {
  browser('browser', 'Browser'),
  audiobookshelf('audiobookshelf', 'Audiobookshelf'),
  shelfplayer('shelfplayer', 'ShelfPlayer'),
  theShelf('theshelf', 'TheShelf');

  const ListeningApp(this.id, this.label);
  final String id;
  final String label;

  String get actionName => switch (this) {
        browser || audiobookshelf => 'Audiobookshelf',
        _ => label,
      };

  static ListeningApp fromId(String? id) => values.firstWhere(
        (app) => app.id == id,
        orElse: () => browser,
      );

  static const ios = [browser, audiobookshelf, shelfplayer];
  static const android = [browser, audiobookshelf, theShelf];
}

/// Empty values inherit instance defaults in personal settings. Resolved link
/// responses always name the chosen app; older servers resolve to the browser.
class ListeningApps {
  final String ios;
  final String android;

  const ListeningApps({this.ios = '', this.android = ''});

  factory ListeningApps.fromJson(dynamic json) => json is Map
      ? ListeningApps(
          ios: json['ios'] is String ? json['ios'] as String : '',
          android: json['android'] is String ? json['android'] as String : '',
        )
      : const ListeningApps();

  Map<String, String> toJson() => {'ios': ios, 'android': android};

  ListeningApp forPlatform(TargetPlatform platform, {required bool isWeb}) {
    if (isWeb) return ListeningApp.browser;
    final chosen = ListeningApp.fromId(switch (platform) {
      TargetPlatform.iOS => ios,
      TargetPlatform.android => android,
      _ => null,
    });
    final supported = switch (platform) {
      TargetPlatform.iOS => ListeningApp.ios,
      TargetPlatform.android => ListeningApp.android,
      _ => const [ListeningApp.browser],
    };
    return supported.contains(chosen) ? chosen : ListeningApp.browser;
  }
}
