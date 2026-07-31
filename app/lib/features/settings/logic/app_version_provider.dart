import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:package_info_plus/package_info_plus.dart';

/// The running build's identity, as Settings and the About sheet show it.
class AppVersion {
  const AppVersion({required this.version, required this.buildNumber});

  /// Marketing version, e.g. `0.1.0` (`pubspec.yaml`'s version, or whatever
  /// `--build-name` the release build was stamped with).
  final String version;

  /// Per-channel build counter: the TestFlight build for iOS, the Play build
  /// for Android, and the image's CI run for the web bundle the server serves
  /// (stamped by `--build-number` in the Dockerfile). Empty when a build
  /// carried no number.
  final String buildNumber;

  /// e.g. `Version 0.1.0 (238)`.
  String get label =>
      buildNumber.isEmpty ? 'Version $version' : 'Version $version ($buildNumber)';
}

/// Reads the build identity once per app run. `PackageInfo` sources it from
/// the platform bundle natively and from `version.json` on web.
final appVersionProvider = FutureProvider<AppVersion>((ref) async {
  final info = await PackageInfo.fromPlatform();
  return AppVersion(version: info.version, buildNumber: info.buildNumber);
});
