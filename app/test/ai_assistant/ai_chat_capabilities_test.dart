import 'package:cantinarr/features/ai_assistant/data/ai_chat_capabilities.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_settings_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('movie-only requesters see their media and no admin promises', () {
    final caps = AiChatCapabilities.fromJson({
      'discover_media_types': ['movie'],
      'request_media_types': ['movie'],
      'check_availability': true,
    });
    expect(caps.welcomeMessage, contains('discover and request movies'));
    expect(caps.welcomeMessage, contains('check what\'s available'));
    for (final unsupported in ['TV', 'books', 'music', 'manage', 'configure']) {
      expect(caps.welcomeMessage, isNot(contains(unsupported)));
    }
    expect(caps.suggestions, ['Recommend a movie', 'What\'s available in my library?']);
  });

  test('discovery and requests describe distinct media permissions', () {
    final caps = AiChatCapabilities.fromJson({
      'discover_media_types': ['movie', 'tv', 'book'],
      'request_media_types': ['book'],
    });
    expect(caps.welcomeMessage, contains('discover movies, TV shows, and books'));
    expect(caps.welcomeMessage, contains('request books'));
    expect(caps.welcomeMessage, isNot(contains('request movies')));
  });

  test('read-only downloads do not promise management or disabled settings', () {
    final caps = AiChatCapabilities.fromJson({
      'check_downloads': true,
      'manage_downloads': false,
      'configure_services': false,
    });
    expect(caps.welcomeMessage, contains('check downloads'));
    expect(caps.welcomeMessage, isNot(contains('manage')));
    expect(caps.welcomeMessage, isNot(contains('configure')));
  });

  test('older servers and unknown capability values stay neutral', () {
    expect(AiSettings.fromJson({}).chatCapabilities, isNull);
    expect(AiSettings.fromJson({'chat_capabilities': null}).chatCapabilities, isNull);
    final settings = AiSettings.fromJson({
      'chat_capabilities': {
        'discover_media_types': ['unknown', 1],
        'manage_downloads': 'true',
      },
    });
    expect(settings.chatCapabilities!.welcomeMessage, aiChatWelcomeFallback);
    expect(settings.chatCapabilities!.suggestions, isEmpty);
  });
}
