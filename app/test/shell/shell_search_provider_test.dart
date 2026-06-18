import 'package:cantinarr/features/shell/logic/shell_search_provider.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('isAiPromptQuery', () {
    test('detects obvious AI prompts', () {
      expect(isAiPromptQuery('What should I watch tonight?'), true);
      expect(isAiPromptQuery('recommend sci-fi movies'), true);
      expect(isAiPromptQuery('find me shows like Severance'), true);
      expect(isAiPromptQuery('is The Matrix worth watching'), true);
    });

    test('keeps title-like searches in normal search', () {
      expect(isAiPromptQuery('Severance'), false);
      expect(isAiPromptQuery('Once Upon a Time in Hollywood'), false);
      expect(isAiPromptQuery('What We Do in the Shadows'), false);
      expect(isAiPromptQuery('How I Met Your Mother'), false);
      expect(isAiPromptQuery('Who Framed Roger Rabbit'), false);
    });
  });
}
