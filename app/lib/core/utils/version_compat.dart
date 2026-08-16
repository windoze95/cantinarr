import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Client/server version-skew floors. Warn-only by design — a floor violation
/// shows a banner, never blocks; a hard block would be a deliberate future
/// escalation, not a side effect of bumping a constant here.

/// The oldest server this app build fully supports ("0.0.0" = no floor).
/// Server-side twin: MinAppVersion in server/internal/version/version.go —
/// raise either only alongside the breaking change that forces it.
const String minServerVersion = '0.0.0';

/// [minServerVersion] behind a provider so widget tests can raise the floor.
final minServerVersionProvider = Provider<String>((_) => minServerVersion);

/// A lenient semver triple. Mirrors the Go server's update.parseVersion
/// exactly: optional v/V prefix, everything from the first `-`/`+` ignored
/// (so a git-describe stamp like `v0.1.0-12-gabc1234` compares as its base
/// release), missing components read as 0. Null for non-numeric input
/// ("dev", "latest", "pr-42", bare SHAs) — callers never warn on null.
({int major, int minor, int patch})? parseLenientVersion(String? raw) {
  var s = raw?.trim() ?? '';
  if (s.startsWith('v') || s.startsWith('V')) s = s.substring(1);
  final cut = s.indexOf(RegExp(r'[-+]'));
  if (cut >= 0) s = s.substring(0, cut);
  if (s.isEmpty) return null;
  final parts = s.split('.');
  final numbers = <int>[0, 0, 0];
  for (var i = 0; i < parts.length && i < 3; i++) {
    final n = int.tryParse(parts[i]);
    if (n == null) return null;
    numbers[i] = n;
  }
  return (major: numbers[0], minor: numbers[1], patch: numbers[2]);
}

/// True only when both sides parse and [version] is strictly below [floor].
/// Unparseable input on either side means "don't know" — never warn on it.
bool isBelowVersionFloor(String? version, String? floor) {
  final v = parseLenientVersion(version);
  final f = parseLenientVersion(floor);
  if (v == null || f == null) return false;
  if (v.major != f.major) return v.major < f.major;
  if (v.minor != f.minor) return v.minor < f.minor;
  return v.patch < f.patch;
}

enum VersionSkewWarning {
  /// This app build is older than the server's advertised floor. The viewer
  /// can fix it themselves (update the app), so everyone sees it.
  appTooOld,

  /// The server is older than this app's floor. Only an admin can fix it.
  serverTooOld,
}

/// Decides which skew warning (if any) to show. Pure so the gating is unit
/// testable, including the web skip: the web app is served by the server
/// itself — by construction the same build, and its pubspec marketing version
/// is unreliable — so [appTooOld] never applies on web. [appTooOld] wins when
/// both hold because it's the one the viewing user can act on.
VersionSkewWarning? evaluateVersionSkew({
  required bool isWeb,
  required bool isAdmin,
  required String? appVersion,
  required String? serverVersion,
  required String? serverMinAppVersion,
  required String minServerVersionFloor,
}) {
  if (!isWeb && isBelowVersionFloor(appVersion, serverMinAppVersion)) {
    return VersionSkewWarning.appTooOld;
  }
  if (isAdmin && isBelowVersionFloor(serverVersion, minServerVersionFloor)) {
    return VersionSkewWarning.serverTooOld;
  }
  return null;
}
