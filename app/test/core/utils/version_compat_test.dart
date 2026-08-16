import 'package:cantinarr/core/utils/version_compat.dart';
import 'package:flutter_test/flutter_test.dart';

/// Pins the lenient parse/compare to the Go server's update.parseVersion
/// semantics (the two sides must agree on what a version string means), and
/// the skew gating — including the web skip, which widget tests can't reach
/// because kIsWeb is compile-time in the VM.
void main() {
  group('parseLenientVersion', () {
    const parses = {
      '1.2.3': (major: 1, minor: 2, patch: 3),
      'v1.2.3': (major: 1, minor: 2, patch: 3),
      'V2.5': (major: 2, minor: 5, patch: 0),
      '1.3': (major: 1, minor: 3, patch: 0),
      '1.2.4-rc1': (major: 1, minor: 2, patch: 4),
      // The git-describe stamp CI bakes into latest/preview images.
      'v0.1.0-12-gabc1234': (major: 0, minor: 1, patch: 0),
      '1.2.3+45': (major: 1, minor: 2, patch: 3),
      ' 1.2.3 ': (major: 1, minor: 2, patch: 3),
    };
    parses.forEach((input, want) {
      test('parses $input', () {
        expect(parseLenientVersion(input), want);
      });
    });

    for (final input in ['dev', 'latest', 'pr-42', 'abc1234', '', null]) {
      test('rejects $input', () {
        expect(parseLenientVersion(input), isNull);
      });
    }
  });

  group('isBelowVersionFloor', () {
    test('strictly below on each component', () {
      expect(isBelowVersionFloor('1.2.3', '1.2.4'), isTrue);
      expect(isBelowVersionFloor('1.2.3', '1.3.0'), isTrue);
      expect(isBelowVersionFloor('1.2.3', '2.0.0'), isTrue);
      expect(isBelowVersionFloor('1.2.3', '1.2.3'), isFalse);
      expect(isBelowVersionFloor('1.2.4', '1.2.3'), isFalse);
      expect(isBelowVersionFloor('2.0.0', '1.9.9'), isFalse);
    });

    test('describe suffixes compare as their base release', () {
      expect(isBelowVersionFloor('v0.1.0-12-gabc1234', '0.1.1'), isTrue);
      expect(isBelowVersionFloor('v0.1.1-3-gdef5678', '0.1.1'), isFalse);
    });

    test('either side unparseable never warns', () {
      expect(isBelowVersionFloor('dev', '1.0.0'), isFalse);
      expect(isBelowVersionFloor('1.0.0', 'garbage'), isFalse);
      expect(isBelowVersionFloor(null, '1.0.0'), isFalse);
      expect(isBelowVersionFloor('1.0.0', null), isFalse);
    });

    test('the dormant 0.0.0 floor never warns', () {
      expect(isBelowVersionFloor('0.0.1', '0.0.0'), isFalse);
      expect(isBelowVersionFloor('dev', '0.0.0'), isFalse);
    });
  });

  group('evaluateVersionSkew', () {
    VersionSkewWarning? eval({
      bool isWeb = false,
      bool isAdmin = false,
      String? appVersion = '1.0.0',
      String? serverVersion = '1.0.0',
      String? serverMinAppVersion = '0.0.0',
      String minServerVersionFloor = '0.0.0',
    }) =>
        evaluateVersionSkew(
          isWeb: isWeb,
          isAdmin: isAdmin,
          appVersion: appVersion,
          serverVersion: serverVersion,
          serverMinAppVersion: serverMinAppVersion,
          minServerVersionFloor: minServerVersionFloor,
        );

    test('dormant floors warn nobody', () {
      expect(eval(), isNull);
      expect(eval(isAdmin: true), isNull);
    });

    test('app below the server floor warns everyone — except on web', () {
      expect(eval(serverMinAppVersion: '2.0.0'),
          VersionSkewWarning.appTooOld);
      expect(eval(serverMinAppVersion: '2.0.0', isAdmin: true),
          VersionSkewWarning.appTooOld);
      expect(eval(serverMinAppVersion: '2.0.0', isWeb: true), isNull,
          reason: 'the bundled web client is by construction the server\'s '
              'own build; its marketing version is unreliable');
    });

    test('server below the app floor warns admins only', () {
      expect(eval(minServerVersionFloor: '2.0.0', isAdmin: true),
          VersionSkewWarning.serverTooOld);
      expect(eval(minServerVersionFloor: '2.0.0'), isNull,
          reason: 'a requester cannot update the server');
    });

    test('server-too-old still applies on web', () {
      expect(eval(minServerVersionFloor: '2.0.0', isAdmin: true, isWeb: true),
          VersionSkewWarning.serverTooOld);
    });

    test('app-too-old wins when both floors are violated', () {
      expect(
        eval(
          isAdmin: true,
          serverMinAppVersion: '2.0.0',
          minServerVersionFloor: '2.0.0',
        ),
        VersionSkewWarning.appTooOld,
      );
    });

    test('nulls while loading warn nobody', () {
      expect(eval(appVersion: null, serverMinAppVersion: '2.0.0'), isNull);
      expect(
          eval(
            isAdmin: true,
            serverVersion: null,
            minServerVersionFloor: '2.0.0',
          ),
          isNull);
    });
  });
}
