/// Server status returned by GET /api/auth/status.
class ServerStatus {
  final bool needsSetup;
  final bool webAuthnAvailable;

  const ServerStatus({
    required this.needsSetup,
    this.webAuthnAvailable = false,
  });

  factory ServerStatus.fromJson(Map<String, dynamic> json) => ServerStatus(
        needsSetup: json['needs_setup'] as bool? ?? true,
        webAuthnAvailable: json['webauthn_available'] as bool? ?? false,
      );
}
