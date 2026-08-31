import 'package:cantinarr/core/network/long_request_options.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('longRequestOptions', () {
    test('native: raises receiveTimeout only, connectTimeout inherits base',
        () {
      final options = longRequestOptions(isWeb: false);
      expect(options.receiveTimeout, const Duration(seconds: 120));
      // Null means Options.compose falls back to the base connectTimeout, so
      // an unreachable server still fails fast on native.
      expect(options.connectTimeout, isNull);
    });

    test('web: raises connectTimeout to match — it bounds time-to-first-'
        'headers there', () {
      final options = longRequestOptions(isWeb: true);
      expect(options.receiveTimeout, const Duration(seconds: 120));
      expect(options.connectTimeout, const Duration(seconds: 120));
    });

    test('a custom timeout flows to both web timeouts', () {
      final options = longRequestOptions(
        timeout: const Duration(seconds: 60),
        isWeb: true,
      );
      expect(options.receiveTimeout, const Duration(seconds: 60));
      expect(options.connectTimeout, const Duration(seconds: 60));
    });
  });
}
