import 'package:cantinarr/features/auth/data/connection_input.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('an invitation is redeemed intact, including its encoded server path', () {
    const link = 'cantinarr://connect?token=invitation'
        '&server=https%3A%2F%2Fcantinarr.example.com%2Fmedia';
    final input = ConnectionInput.parse('  $link  ');
    expect(input.isLink, isTrue);
    expect(input.value, link);
  });

  for (final address in [
    'cantinarr.example.com',
    '192.168.1.10:8585',
    'localhost:8585',
    'http://home.local:8585',
    'HTTPS://cantinarr.example.com/media/',
    'https://[::1]:8585/media',
    'https://example:password@home.test',
    'https://home.test/media?token=proxy-access',
  ]) {
    test('keeps the original address for scheme probing: $address', () {
      final input = ConnectionInput.parse('  $address  ');
      expect(input.isLink, isFalse);
      expect(input.value, address);
    });
  }

  for (final link in [
    'cantinarr://connect',
    'cantinarr://connect?token=private-invitation',
    'cantinarr://connect?server=https%3A%2F%2Fhome.test',
    'cantinarr://connect?token=&server=https%3A%2F%2Fhome.test',
    'cantinarr://connect?token=private-invitation&server=',
    'cantinarr://passkeys?token=private-invitation&server=https%3A%2F%2Fhome.test',
    'cantinarr://connect?token=private-invitation&server=ftp%3A%2F%2Fhome.test',
    'https://connect?token=private-invitation&server=https%3A%2F%2Fhome.test',
    'cantinarr://connect?token=private-invitation&server=%E0%A4%A',
  ]) {
    test('an invalid invitation never becomes an address: $link', () {
      expect(() => ConnectionInput.parse(link), throwsA(
        isA<FormatException>().having((e) => e.message, 'safe message',
            isNot(contains('private-invitation'))),
      ));
    });
  }

  for (final address in ['', 'http://', 'not an address', 'plex://home']) {
    test('rejects invalid addresses: $address', () {
      expect(() => ConnectionInput.parse(address), throwsFormatException);
    });
  }
}
