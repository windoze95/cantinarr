import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('ConnectTokenResponse', () {
    test('parses origin_source', () {
      final resp = ConnectTokenResponse.fromJson(const {
        'link': 'cantinarr://connect?token=abc&server=https%3A%2F%2Fx',
        'expires_at': '2026-08-26T00:00:00Z',
        'origin_source': 'external_address',
      });
      expect(resp.originSource, 'external_address');
    });

    test('an older server sends no origin_source, which must not hint', () {
      final resp = ConnectTokenResponse.fromJson(const {
        'link': 'cantinarr://connect?token=abc&server=http%3A%2F%2Fx',
        'expires_at': '2026-08-26T00:00:00Z',
      });
      expect(resp.originSource, '');
    });
  });

  // The server percent-encodes the address it embeds (url.QueryEscape), and
  // the phone's deep-link handler keys on scheme + host before reading the
  // parameters. These are verbatim wire strings a running server produced, so
  // a future change to link construction that a phone could not follow fails
  // here instead of on someone's device.
  group('connect link wire format', () {
    void expectRedeemable(String link, String wantServer) {
      final uri = Uri.parse(link);
      expect(uri.scheme, 'cantinarr');
      expect(uri.host, 'connect'); // what app.dart routes on
      expect(uri.queryParameters['token'], isNotEmpty);
      expect(uri.queryParameters['server'], wantServer);
    }

    test('a link built from an external address points at that address', () {
      expectRedeemable(
        'cantinarr://connect?token=2067660b6c020fb4976833de927962c3029fcfe7'
        '18b63d1ba6a5b047c1dbff04&server=http%3A%2F%2F192.168.20.158%3A8585',
        'http://192.168.20.158:8585',
      );
    });

    test('an https external address survives encoding intact', () {
      expectRedeemable(
        'cantinarr://connect?token=abc123&server=https%3A%2F%2Fcantinarr.example.com',
        'https://cantinarr.example.com',
      );
    });

    test('the unconfigured fallback still carries the app-side address', () {
      expectRedeemable(
        'cantinarr://connect?token=8319eec76933b29e4953bde4d5b8033f9d24b71f'
        'c5f0a5f1c4a12aa3eee132db&server=http%3A%2F%2F192.168.20.55%3A8585',
        'http://192.168.20.55:8585',
      );
    });
  });
}
