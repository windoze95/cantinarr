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

    test('handles legacy AI status without provider metadata', () {
      final status = CredentialsStatus.fromJson({
        'anthropic_key': true,
        'ai': true,
      });

      expect(status.isConfigured('anthropic_key'), true);
      expect(status.ai.provider, 'anthropic');
      expect(status.ai.model, 'claude-opus-4-8');
      expect(status.ai.providers, isEmpty);
    });

    test('parses a per-user OAuth provider without a credential key', () {
      final status = CredentialsStatus.fromJson({
        'credentials': const <String, bool>{},
        'ai': {
          'config': {
            'provider': 'codex',
            'model': 'gpt-5.4',
          },
          'providers': [
            {
              'id': 'codex',
              'label': 'ChatGPT (Codex)',
              'auth_type': 'user_oauth',
              'models': [
                {'id': 'gpt-5.4', 'label': 'GPT-5.4'},
              ],
            },
          ],
        },
      });

      final provider = status.ai.providers.single;
      expect(provider.id, 'codex');
      expect(provider.credentialKey, isEmpty);
      expect(provider.authType, 'user_oauth');
      expect(provider.usesUserOAuth, isTrue);
    });
  });
}
