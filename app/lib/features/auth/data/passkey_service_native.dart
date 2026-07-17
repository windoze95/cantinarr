import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:passkeys_platform_interface/passkeys_platform_interface.dart';
import 'package:passkeys_platform_interface/types/types.dart';

PasskeysPlatform get _platform => PasskeysPlatform.instance;

bool isAvailable() => false;

String platformKind() {
  if (Platform.isAndroid) return 'android';
  if (Platform.isIOS) return 'ios';
  if (Platform.isWindows) return 'windows';
  if (Platform.isMacOS) return 'macos';
  if (Platform.isLinux) return 'linux';
  return 'unsupported';
}

Future<bool> isAvailableAsync() async {
  if (!Platform.isAndroid && !Platform.isIOS && !Platform.isWindows) {
    return false;
  }
  try {
    final availability = await _platform.getAvailability();
    return availability.hasPasskeySupport;
  } catch (_) {
    return false;
  }
}

Future<Map<String, dynamic>> create(Map<String, dynamic> options) async {
  try {
    await _platform.cancelCurrentAuthenticatorOperation();
    final publicKey = normalizeCreationOptions(_publicKeyOptions(options));
    final request = RegisterRequestType.fromJson(publicKey);
    final response = await _platform.register(request);
    return response.toJson();
  } on PlatformException catch (e) {
    throw Exception(messageForPlatformException(e, isLogin: false));
  }
}

Future<Map<String, dynamic>> get(Map<String, dynamic> options) async {
  try {
    await _platform.cancelCurrentAuthenticatorOperation();
    final publicKey = normalizeRequestOptions(_publicKeyOptions(options));
    final request = AuthenticateRequestType.fromJson(
      publicKey,
      mediation: MediationType.Required,
      preferImmediatelyAvailableCredentials: false,
    );
    final response = await _platform.authenticate(request);
    return response.toJson();
  } on PlatformException catch (e) {
    throw Exception(messageForPlatformException(e, isLogin: true));
  }
}

Map<String, dynamic> _publicKeyOptions(Map<String, dynamic> options) {
  final publicKey = options['publicKey'] ?? options;
  if (publicKey is Map<String, dynamic>) {
    return publicKey;
  }
  if (publicKey is Map) {
    return publicKey.cast<String, dynamic>();
  }
  throw const FormatException('Missing passkey options');
}

/// Fills in the optional WebAuthn creation fields the native authenticator
/// libraries require (exposed for tests; behavior-identical to the previous
/// private helper).
@visibleForTesting
Map<String, dynamic> normalizeCreationOptions(Map<String, dynamic> options) {
  final normalized = Map<String, dynamic>.from(options);
  normalized['excludeCredentials'] =
      credentialDescriptors(normalized['excludeCredentials']);

  final authenticatorSelection = normalized['authenticatorSelection'];
  if (authenticatorSelection is Map) {
    final selection = Map<String, dynamic>.from(authenticatorSelection);
    final residentKey = selection['residentKey'] as String?;
    selection['requireResidentKey'] ??= residentKey == 'required';
    selection['residentKey'] ??=
        selection['requireResidentKey'] == true ? 'required' : 'preferred';
    selection['userVerification'] ??= 'preferred';
    normalized['authenticatorSelection'] = selection;
  }

  return normalized;
}

/// Normalizes WebAuthn request (login) options: defaults credential
/// transports and drops an empty allowCredentials list entirely (exposed for
/// tests; behavior-identical to the previous private helper).
@visibleForTesting
Map<String, dynamic> normalizeRequestOptions(Map<String, dynamic> options) {
  final normalized = Map<String, dynamic>.from(options);
  final allowCredentials = credentialDescriptors(
    normalized['allowCredentials'],
  );
  if (allowCredentials.isEmpty) {
    normalized.remove('allowCredentials');
  } else {
    normalized['allowCredentials'] = allowCredentials;
  }
  return normalized;
}

/// Coerces a raw excludeCredentials/allowCredentials value into descriptor
/// maps with `transports` always present (exposed for tests;
/// behavior-identical to the previous private helper).
@visibleForTesting
List<Map<String, dynamic>> credentialDescriptors(Object? value) {
  if (value is! List) {
    return const [];
  }
  return value.whereType<Map>().map((credential) {
    final descriptor = Map<String, dynamic>.from(credential);
    descriptor['transports'] ??= <String>[];
    return descriptor;
  }).toList();
}

/// Maps a passkeys plugin [PlatformException] code to a user-facing message
/// (exposed for tests; behavior-identical to the previous private helper).
@visibleForTesting
String messageForPlatformException(
  PlatformException error, {
  required bool isLogin,
}) {
  switch (error.code) {
    case 'cancelled':
      return isLogin
          ? 'Passkey sign-in was cancelled.'
          : 'Passkey creation was cancelled.';
    case 'domain-not-associated':
      return 'This server is not associated with this app for native passkeys.';
    case 'deviceNotSupported':
    case 'android-passkey-unsupported':
      return 'This device does not support passkeys.';
    case 'android-missing-google-sign-in':
      return 'Sign in to a Google account before creating Android passkeys.';
    case 'android-sync-account-not-available':
      return 'The Android passkey sync account is not available. Try again after restarting the device.';
    case 'android-no-create-option':
      return 'No passkey provider is available. Enable a credential provider such as Bitwarden or Google Password Manager.';
    case 'no-credentials-available':
    case 'android-no-credential':
      return 'No passkey was found for this server.';
    case 'exclude-credentials-match':
      return 'A matching passkey is already registered on this device.';
    case 'android-timeout':
    case 'ios-security-key-timeout':
      return 'The passkey prompt timed out.';
    default:
      if (error.code.startsWith('android-unhandled') ||
          error.code.startsWith('ios-unhandled')) {
        return error.message ?? 'Passkey authentication failed.';
      }
      return 'Passkey authentication failed.';
  }
}
