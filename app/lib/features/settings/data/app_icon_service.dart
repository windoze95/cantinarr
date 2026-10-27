import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';

enum AppIcon {
  playSun('play-sun', 'Play Sun', 'app_icon_play_sun.png'),
  playSign('play-sign', 'Play Sign', 'app_icon_play_sign.png'),
  playDome('play-dome', 'Play Dome', 'app_icon_play_dome.png'),
  original('original', 'Original', 'app_icon_original.png');

  const AppIcon(this.id, this.label, this.asset);
  final String id;
  final String label;
  final String asset;

  static AppIcon fromId(String? id) => values.firstWhere(
        (icon) => icon.id == id,
        orElse: () => playSun,
      );
}

bool get appIconsVisible =>
    !kIsWeb &&
    (defaultTargetPlatform == TargetPlatform.iOS ||
        defaultTargetPlatform == TargetPlatform.android);

/// The operating system owns the selection, including persistence across
/// restarts and updates. Never report a choice saved before the OS accepts it.
class AppIconService {
  static const channel = MethodChannel('codes.julian.cantinarr/app_icon');

  Future<bool> isSupported() async =>
      await channel.invokeMethod<bool>('isSupported') ?? false;

  Future<AppIcon> current() async =>
      AppIcon.fromId(await channel.invokeMethod<String>('getIcon'));

  Future<AppIcon> select(AppIcon icon) async {
    await channel.invokeMethod<void>('setIcon', {'icon': icon.id});
    return current();
  }
}
