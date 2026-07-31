import 'package:cantinarr/features/auth/data/passkey_service_native.dart'
    as passkeys;
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:passkeys_platform_interface/passkeys_platform_interface.dart';
import 'package:passkeys_platform_interface/types/types.dart';

/// Exercises the native passkey glue: the WebAuthn option normalization the
/// authenticator libraries depend on, the PlatformException code → message
/// table, and — through a [PasskeysPlatform] fake — that create()/get() hand
/// the platform a well-formed, normalized typed request.
void main() {
  group('normalizeCreationOptions', () {
    test('defaults excludeCredentials to an empty list when absent', () {
      final normalized = passkeys.normalizeCreationOptions({
        'challenge': 'Y2hhbGxlbmdl',
      });
      expect(normalized['excludeCredentials'], isEmpty);
    });

    test('defaults transports on descriptors and preserves base64url ids', () {
      final normalized = passkeys.normalizeCreationOptions({
        'excludeCredentials': [
          {'type': 'public-key', 'id': 'dTlaVy1hYmNfMTIz-_'},
          {
            'type': 'public-key',
            'id': 'b3RoZXI',
            'transports': ['usb'],
          },
        ],
      });
      final descriptors = normalized['excludeCredentials'] as List;
      expect(descriptors, hasLength(2));
      expect(descriptors[0]['id'], 'dTlaVy1hYmNfMTIz-_',
          reason: 'base64url ids must pass through untouched');
      expect(descriptors[0]['transports'], isEmpty);
      expect(descriptors[1]['transports'], ['usb'],
          reason: 'existing transports must be preserved');
    });

    test("residentKey 'required' implies requireResidentKey", () {
      final normalized = passkeys.normalizeCreationOptions({
        'authenticatorSelection': {'residentKey': 'required'},
      });
      final selection = normalized['authenticatorSelection'] as Map;
      expect(selection['requireResidentKey'], isTrue);
      expect(selection['residentKey'], 'required');
      expect(selection['userVerification'], 'preferred');
    });

    test("requireResidentKey true implies residentKey 'required'", () {
      final normalized = passkeys.normalizeCreationOptions({
        'authenticatorSelection': {'requireResidentKey': true},
      });
      final selection = normalized['authenticatorSelection'] as Map;
      expect(selection['residentKey'], 'required');
    });

    test('an empty selection gets the preferred defaults', () {
      final normalized = passkeys.normalizeCreationOptions({
        'authenticatorSelection': <String, dynamic>{},
      });
      final selection = normalized['authenticatorSelection'] as Map;
      expect(selection['requireResidentKey'], isFalse);
      expect(selection['residentKey'], 'preferred');
      expect(selection['userVerification'], 'preferred');
    });

    test('explicit selection values are never overwritten', () {
      final normalized = passkeys.normalizeCreationOptions({
        'authenticatorSelection': {
          'requireResidentKey': false,
          'residentKey': 'discouraged',
          'userVerification': 'required',
        },
      });
      final selection = normalized['authenticatorSelection'] as Map;
      expect(selection['requireResidentKey'], isFalse);
      expect(selection['residentKey'], 'discouraged');
      expect(selection['userVerification'], 'required');
    });

    test('a missing authenticatorSelection stays missing', () {
      final normalized = passkeys.normalizeCreationOptions({
        'challenge': 'Y2hhbGxlbmdl',
      });
      expect(normalized.containsKey('authenticatorSelection'), isFalse);
    });

    test('does not mutate the input map', () {
      final input = {
        'excludeCredentials': [
          {'type': 'public-key', 'id': 'aWQ'},
        ],
      };
      passkeys.normalizeCreationOptions(input);
      final descriptor = (input['excludeCredentials'] as List).first as Map;
      expect(descriptor.containsKey('transports'), isFalse);
    });
  });

  group('normalizeRequestOptions', () {
    test('removes an empty or missing allowCredentials list', () {
      expect(
        passkeys.normalizeRequestOptions({'allowCredentials': <dynamic>[]}),
        isNot(contains('allowCredentials')),
      );
      expect(
        passkeys.normalizeRequestOptions({'challenge': 'Y2hhbGxlbmdl'}),
        isNot(contains('allowCredentials')),
      );
      expect(
        passkeys.normalizeRequestOptions({'allowCredentials': 'garbage'}),
        isNot(contains('allowCredentials')),
      );
    });

    test('keeps entries and defaults their transports', () {
      final normalized = passkeys.normalizeRequestOptions({
        'allowCredentials': [
          {'type': 'public-key', 'id': 'Zmlyc3Q-_'},
        ],
      });
      final credentials = normalized['allowCredentials'] as List;
      expect(credentials.single['id'], 'Zmlyc3Q-_');
      expect(credentials.single['transports'], isEmpty);
    });
  });

  group('credentialDescriptors', () {
    test('coerces non-lists to empty and filters non-map entries', () {
      expect(passkeys.credentialDescriptors(null), isEmpty);
      expect(passkeys.credentialDescriptors('nope'), isEmpty);
      expect(
        passkeys.credentialDescriptors([
          'junk',
          42,
          {'type': 'public-key', 'id': 'aWQ'},
        ]),
        hasLength(1),
      );
    });
  });

  group('messageForPlatformException', () {
    String messageFor(String code, {bool isLogin = false, String? message}) =>
        passkeys.messageForPlatformException(
          PlatformException(code: code, message: message),
          isLogin: isLogin,
        );

    test('cancelled distinguishes sign-in from creation', () {
      expect(messageFor('cancelled', isLogin: true),
          'Passkey sign-in was cancelled.');
      expect(messageFor('cancelled'), 'Passkey creation was cancelled.');
    });

    test('maps every known android-*/ios-* code', () {
      final expected = <String, String>{
        'domain-not-associated':
            'This server is not associated with this app for native passkeys.',
        'deviceNotSupported': 'This device does not support passkeys.',
        'android-passkey-unsupported':
            'This device does not support passkeys.',
        'android-missing-google-sign-in':
            'Sign in to a Google account before creating Android passkeys.',
        'android-sync-account-not-available':
            'The Android passkey sync account is not available. Try again after restarting the device.',
        'android-no-create-option':
            'No passkey provider is available. Enable a credential provider such as Bitwarden or Google Password Manager.',
        'no-credentials-available': 'No passkey was found for this server.',
        'android-no-credential': 'No passkey was found for this server.',
        'exclude-credentials-match':
            'A matching passkey is already registered on this device.',
        'android-timeout': 'The passkey prompt timed out.',
        'ios-security-key-timeout': 'The passkey prompt timed out.',
      };
      expected.forEach((code, message) {
        expect(messageFor(code), message, reason: code);
      });
    });

    test('unhandled codes surface the platform message when present', () {
      expect(
        messageFor('android-unhandled-CreateCredentialUnknownException',
            message: 'Native detail'),
        'Native detail',
      );
      expect(messageFor('ios-unhandled', message: null),
          'Passkey authentication failed.');
    });

    test('unknown codes fall back to the generic message', () {
      expect(messageFor('surprise-code', message: 'ignored'),
          'Passkey authentication failed.');
    });
  });

  group('create/get through a PasskeysPlatform fake', () {
    late _FakePasskeysPlatform platform;
    late PasskeysPlatform original;

    setUp(() {
      original = PasskeysPlatform.instance;
      platform = _FakePasskeysPlatform();
      PasskeysPlatform.instance = platform;
    });

    tearDown(() {
      PasskeysPlatform.instance = original;
    });

    Map<String, dynamic> creationOptions() => {
          'publicKey': {
            'challenge': 'Y2hhbGxlbmdlLTEyMw',
            'rp': {'id': 'media.example.com', 'name': 'Cantinarr'},
            'user': {
              'id': 'dXNlci0x',
              'name': 'tester',
              'displayName': 'Tester',
            },
            'excludeCredentials': [
              {'type': 'public-key', 'id': 'ZXhpc3Rpbmc-_'},
            ],
            'authenticatorSelection': {'residentKey': 'required'},
          },
        };

    test('create cancels any pending operation and registers a normalized '
        'request', () async {
      final response = await passkeys.create(creationOptions());

      expect(platform.cancelCalls, 1);
      final request = platform.lastRegister!;
      expect(request.challenge, 'Y2hhbGxlbmdlLTEyMw');
      expect(request.relyingParty.id, 'media.example.com');
      expect(request.user.name, 'tester');
      expect(request.excludeCredentials.single.id, 'ZXhpc3Rpbmc-_');
      expect(request.excludeCredentials.single.transports, isEmpty,
          reason: 'missing transports must be defaulted, or the typed '
              'deserialization rejects the descriptor');
      expect(request.authSelectionType?.requireResidentKey, isTrue);
      expect(response['id'], 'cred-1');
    });

    test('create accepts options without a publicKey wrapper', () async {
      final unwrapped =
          creationOptions()['publicKey']! as Map<String, dynamic>;
      await passkeys.create(unwrapped);
      expect(platform.lastRegister, isNotNull);
    });

    test('create rejects unusable options with a FormatException', () async {
      await expectLater(
        passkeys.create({'publicKey': 'garbage'}),
        throwsFormatException,
      );
    });

    test('create maps a PlatformException to its user-facing message',
        () async {
      platform.registerError =
          PlatformException(code: 'exclude-credentials-match');
      await expectLater(
        passkeys.create(creationOptions()),
        throwsA(isA<Exception>().having(
          (e) => e.toString(),
          'message',
          contains('A matching passkey is already registered on this device.'),
        )),
      );
    });

    test('get authenticates with required mediation and drops empty '
        'allowCredentials', () async {
      await passkeys.get({
        'publicKey': {
          'challenge': 'bG9naW4tY2hhbGxlbmdl',
          'rpId': 'media.example.com',
          'allowCredentials': <dynamic>[],
        },
      });

      final request = platform.lastAuthenticate!;
      expect(request.challenge, 'bG9naW4tY2hhbGxlbmdl');
      expect(request.relyingPartyId, 'media.example.com');
      expect(request.allowCredentials, isNull);
      expect(request.mediation, MediationType.Required);
      expect(request.preferImmediatelyAvailableCredentials, isFalse);
    });

    test('get defaults transports on allowed credentials', () async {
      await passkeys.get({
        'publicKey': {
          'challenge': 'bG9naW4tY2hhbGxlbmdl',
          'rpId': 'media.example.com',
          'allowCredentials': [
            {'type': 'public-key', 'id': 'a25vd24'},
          ],
        },
      });

      final credentials = platform.lastAuthenticate!.allowCredentials!;
      expect(credentials.single.id, 'a25vd24');
      expect(credentials.single.transports, isEmpty);
    });

    test('get maps a PlatformException using login wording', () async {
      platform.authenticateError = PlatformException(code: 'cancelled');
      await expectLater(
        passkeys.get({
          'publicKey': {
            'challenge': 'bG9naW4',
            'rpId': 'media.example.com',
          },
        }),
        throwsA(isA<Exception>().having(
          (e) => e.toString(),
          'message',
          contains('Passkey sign-in was cancelled.'),
        )),
      );
    });
  });
}

/// [PasskeysPlatform] fake that records the typed requests the service builds
/// and returns canned WebAuthn responses (or throws a configured error).
class _FakePasskeysPlatform extends PasskeysPlatform {
  RegisterRequestType? lastRegister;
  AuthenticateRequestType? lastAuthenticate;
  Object? registerError;
  Object? authenticateError;
  int cancelCalls = 0;

  @override
  Future<void> cancelCurrentAuthenticatorOperation() async {
    cancelCalls++;
  }

  @override
  Future<AvailabilityType> getAvailability() async =>
      AvailabilityTypeIOS(
        hasPasskeySupport: true,
        isNative: true,
        hasBiometrics: true,
      );

  @override
  Future<RegisterResponseType> register(RegisterRequestType request) async {
    final error = registerError;
    if (error != null) throw error;
    lastRegister = request;
    return const RegisterResponseType(
      id: 'cred-1',
      rawId: 'cred-1',
      clientDataJSON: 'e30',
      attestationObject: 'e30',
      transports: ['internal'],
    );
  }

  @override
  Future<AuthenticateResponseType> authenticate(
    AuthenticateRequestType request,
  ) async {
    final error = authenticateError;
    if (error != null) throw error;
    lastAuthenticate = request;
    return const AuthenticateResponseType(
      id: 'cred-1',
      rawId: 'cred-1',
      clientDataJSON: 'e30',
      authenticatorData: 'e30',
      signature: 'c2ln',
      userHandle: 'dXNlci0x',
    );
  }
}
