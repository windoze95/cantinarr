import 'package:cantinarr/features/settings/data/update_status_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('UpdateStatus.fromJson', () {
    test('parses a full payload', () {
      final s = UpdateStatus.fromJson({
        'update': {
          'current': '1.2.3',
          'latest': '1.3.0',
          'available': true,
          'url': 'https://example.com/releases/tag/v1.3.0',
        },
        'management_url': 'http://tower.local/Docker',
      });
      expect(s.update.current, '1.2.3');
      expect(s.update.latest, '1.3.0');
      expect(s.update.available, isTrue);
      expect(s.update.url, 'https://example.com/releases/tag/v1.3.0');
      expect(s.managementUrl, 'http://tower.local/Docker');
    });

    test('defaults missing fields', () {
      final s = UpdateStatus.fromJson(const {});
      expect(s.update.current, '');
      expect(s.update.latest, '');
      expect(s.update.available, isFalse);
      expect(s.update.url, '');
      expect(s.managementUrl, '');
    });
  });
}
