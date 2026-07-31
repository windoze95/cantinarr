import 'package:cantinarr/features/discover/data/discover_api_service.dart';
import 'package:cantinarr/features/shell/logic/shell_search_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('isAiPromptQuery', () {
    test('detects obvious AI prompts', () {
      expect(isAiPromptQuery('What should I watch tonight?'), true);
      expect(isAiPromptQuery('recommend sci-fi movies'), true);
      expect(isAiPromptQuery('find me shows like Severance'), true);
      expect(isAiPromptQuery('is The Matrix worth watching'), true);
      expect(isAiPromptQuery('films like Dune'), true);
      expect(isAiPromptQuery('something like Severance'), true);
      expect(isAiPromptQuery('similar to The Matrix'), true);
      expect(isAiPromptQuery('anything with Tom Hanks'), true);
      expect(isAiPromptQuery('books like The Martian'), true);
      expect(isAiPromptQuery('what to watch tonight'), true);
      expect(isAiPromptQuery('what to read after Project Hail Mary'), true);
      expect(isAiPromptQuery('where to watch Dune'), true);
      expect(isAiPromptQuery('compare the Dune adaptations'), true);
      expect(isAiPromptQuery('explain the ending of Tenet'), true);
      expect(isAiPromptQuery('top 10 heist movies'), true);
      expect(isAiPromptQuery('any good thrillers from the 90s'), true);
      expect(isAiPromptQuery("i'm in the mood for something scary"), true);
      expect(isAiPromptQuery('i feel like watching a comedy'), true);
      expect(isAiPromptQuery('name a movie where the dog lives'), true);
      expect(isAiPromptQuery('pick something for movie night'), true);
      expect(isAiPromptQuery('list all my unwatched movies'), true);
      expect(isAiPromptQuery('is there a sequel to Tremors'), true);
      expect(isAiPromptQuery('surprise me'), true);
    });

    test('keeps title-like searches in normal search', () {
      expect(isAiPromptQuery('Severance'), false);
      expect(isAiPromptQuery('Once Upon a Time in Hollywood'), false);
      expect(isAiPromptQuery('What We Do in the Shadows'), false);
      expect(isAiPromptQuery('How I Met Your Mother'), false);
      expect(isAiPromptQuery('Who Framed Roger Rabbit'), false);
      // Each of these pins a phrasing the trigger list deliberately leaves
      // out — a broader rule would eat the real title.
      expect(isAiPromptQuery('In the Mood for Love'), false);
      expect(isAiPromptQuery('Looking for Alaska'), false);
      expect(isAiPromptQuery('What Happens in Vegas'), false);
      expect(isAiPromptQuery("What to Expect When You're Expecting"), false);
      expect(isAiPromptQuery('Best in Show'), false);
      expect(isAiPromptQuery('Top Gun'), false);
      expect(isAiPromptQuery('Need for Speed'), false);
      expect(isAiPromptQuery("Something's Gotta Give"), false);
      expect(isAiPromptQuery('Explained'), false);
      expect(isAiPromptQuery('Do the Right Thing'), false);
      expect(isAiPromptQuery('Where the Crawdads Sing'), false);
      expect(isAiPromptQuery('Anything Else'), false);
    });
  });

  test('search mode re-evaluates when an AI prompt is edited into a title', () {
    final notifier = ShellSearchNotifier(
      DiscoverApiService(backendDio: Dio()),
      aiAvailable: true,
    );

    addTearDown(notifier.dispose);

    notifier.updateSearch('What should I watch tonight?');
    expect(notifier.state.searchMode, SearchMode.aiReady);
    expect(notifier.state.isLoadingSearch, true);

    notifier.updateSearch('Severance');
    expect(notifier.state.searchMode, SearchMode.search);
    expect(notifier.state.isLoadingSearch, true);
  });

  test('ai-ready mode sticks while the user appends more text', () {
    final notifier = ShellSearchNotifier(
      DiscoverApiService(backendDio: Dio()),
      aiAvailable: true,
    );

    addTearDown(notifier.dispose);

    notifier.updateSearch('Dune?');
    expect(notifier.state.searchMode, SearchMode.aiReady);

    notifier.updateSearch('Dune? part two');
    expect(notifier.state.searchMode, SearchMode.aiReady);
    expect(notifier.state.isLoadingSearch, true);
  });

  group('enterAiMode', () {
    ShellSearchNotifier makeNotifier({bool aiAvailable = true}) {
      final notifier = ShellSearchNotifier(
        DiscoverApiService(backendDio: Dio()),
        aiAvailable: aiAvailable,
      );
      addTearDown(notifier.dispose);
      return notifier;
    }

    test('is a no-op when AI is unavailable', () {
      final notifier = makeNotifier(aiAvailable: false);

      notifier.enterAiMode();
      expect(notifier.state.searchMode, SearchMode.search);
    });

    test('enters with an empty query and sticks through non-empty edits', () {
      final notifier = makeNotifier();

      notifier.enterAiMode();
      expect(notifier.state.searchMode, SearchMode.aiReady);

      // Title-like text would flip the heuristic back to normal search; the
      // explicit choice must survive it — rewrites and deletions included.
      notifier.updateSearch('Severance');
      expect(notifier.state.searchMode, SearchMode.aiReady);

      notifier.updateSearch('Sev');
      expect(notifier.state.searchMode, SearchMode.aiReady);
    });

    test('emptying the field turns AI mode fully off', () {
      final notifier = makeNotifier();

      notifier.enterAiMode();
      notifier.updateSearch('Severance');
      expect(notifier.state.searchMode, SearchMode.aiReady);

      notifier.updateSearch('');
      expect(notifier.state.searchMode, SearchMode.search);

      // The explicit choice is gone too: a title stays a title.
      notifier.updateSearch('Severance');
      expect(notifier.state.searchMode, SearchMode.search);
    });

    test('keeps already-typed text and its pending fetch alive', () {
      final notifier = makeNotifier();

      notifier.updateSearch('dark comedies');
      expect(notifier.state.searchMode, SearchMode.search);

      notifier.enterAiMode();
      expect(notifier.state.searchMode, SearchMode.aiReady);
      expect(notifier.state.searchQuery, 'dark comedies');
      expect(notifier.state.isLoadingSearch, true);
    });

    test('exitAiMode clears the explicit choice', () {
      final notifier = makeNotifier();

      notifier.enterAiMode();
      notifier.exitAiMode();
      expect(notifier.state.searchMode, SearchMode.search);

      // Back on the heuristic: a title stays a title.
      notifier.updateSearch('Severance');
      expect(notifier.state.searchMode, SearchMode.search);
    });
  });
}
