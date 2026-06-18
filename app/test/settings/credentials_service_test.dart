import 'package:cantinarr/features/settings/data/credentials_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('CredentialsStatus', () {
    test('uses provider metadata from the server', () {
      final status = CredentialsStatus.fromJson({
        'credentials': {
          'anthropic_key': true,
          'openai_key': false,
        },
        'ai': {
          'config': {
            'provider': 'openai',
            'model': 'gpt-5.4-mini',
          },
          'providers': [
            {
              'id': 'openai',
              'label': 'OpenAI',
              'credential_key': 'openai_key',
              'models': [
                {
                  'id': 'gpt-5.4-mini',
                  'label': 'GPT-5.4 mini',
                },
              ],
            },
          ],
        },
      });

      expect(status.isConfigured('anthropic_key'), true);
      expect(status.ai.provider, 'openai');
      expect(status.ai.model, 'gpt-5.4-mini');
      expect(status.ai.providers.single.credentialKey, 'openai_key');
    });

    test('falls back to built-in providers when metadata is missing', () {
      final status = CredentialsStatus.fromJson({
        'anthropic_key': true,
        'ai': true,
      });

      expect(status.isConfigured('anthropic_key'), true);
      expect(status.ai.provider, 'anthropic');
      expect(status.ai.providers, isNotEmpty);
      expect(
        status.ai.providers.map((provider) => provider.id),
        containsAll(['anthropic', 'openai', 'gemini']),
      );
    });
  });
}
