/// Server status returned by GET /api/auth/status.
class ServerStatus {
  final bool ssoAvailable;
  final bool ssoOnly;
  final String ssoProvider;
  final String ssoOrigin;
  final String? ssoError;
  final bool needsSetup;
  final bool webAuthnAvailable;
  final NativePasskeyStatus nativePasskeys;

  const ServerStatus({
    required this.needsSetup,
    this.ssoAvailable = false,
    this.ssoOnly = false,
    this.ssoProvider = 'Single sign-on',
    this.ssoOrigin = '',
    this.ssoError,
    this.webAuthnAvailable = false,
    this.nativePasskeys = const NativePasskeyStatus(),
  });

  factory ServerStatus.fromJson(Map<String, dynamic> json) => ServerStatus(
        needsSetup: json['needs_setup'] as bool? ?? true,
        ssoAvailable: json['sso_available'] as bool? ?? false,
        ssoOnly: json['sso_only'] as bool? ?? false,
        ssoProvider: json['sso_provider'] as String? ?? 'Single sign-on',
        ssoOrigin: json['sso_origin'] as String? ?? '',
        ssoError: json['sso_error'] as String?,
        webAuthnAvailable: json['webauthn_available'] as bool? ?? false,
        nativePasskeys: NativePasskeyStatus.fromJson(
          json['native_passkeys'] as Map<String, dynamic>?,
        ),
      );

  bool supportsPasskeyPlatform(String platformKind) {
    if (!webAuthnAvailable) return false;
    switch (platformKind) {
      case 'web':
        return true;
      case 'android':
        return nativePasskeys.androidConfigured;
      case 'ios':
        return nativePasskeys.appleConfigured;
      case 'windows':
        return nativePasskeys.windowsOriginTrusted;
      default:
        return false;
    }
  }
}

class NativePasskeyStatus {
  final bool appleConfigured;
  final bool androidConfigured;
  final bool windowsOriginTrusted;

  const NativePasskeyStatus({
    this.appleConfigured = false,
    this.androidConfigured = false,
    this.windowsOriginTrusted = false,
  });

  factory NativePasskeyStatus.fromJson(Map<String, dynamic>? json) =>
      NativePasskeyStatus(
        appleConfigured: json?['apple_configured'] as bool? ?? false,
        androidConfigured: json?['android_configured'] as bool? ?? false,
        windowsOriginTrusted: json?['windows_origin_trusted'] as bool? ?? false,
      );
}
