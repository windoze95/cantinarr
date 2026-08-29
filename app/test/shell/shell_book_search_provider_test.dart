import 'dart:convert';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/shell/logic/shell_book_search_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('no active Chaptarr instance short-circuits before any request',
      () async {
    final adapter = _LookupAdapter();
    final container =
        await _makeContainer(authState: _noInstanceState, adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellBookSearchProvider.notifier);
    notifier.updateSearch('meditations');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    expect(
      container.read(shellBookSearchProvider).error,
      BookSearchError.noInstance,
    );
    expect(adapter.lookupRequests, 0);
  });

  test('a 403 response classifies as forbidden', () async {
    final adapter = _LookupAdapter(statusCode: 403);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellBookSearchProvider.notifier);
    notifier.updateSearch('meditations');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    expect(
      container.read(shellBookSearchProvider).error,
      BookSearchError.forbidden,
    );
  });

  test('a 500 response classifies as requestFailed', () async {
    final adapter = _LookupAdapter(statusCode: 500);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellBookSearchProvider.notifier);
    notifier.updateSearch('meditations');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    expect(
      container.read(shellBookSearchProvider).error,
      BookSearchError.requestFailed,
    );
  });

  test('an empty result list is a successful, matched-nothing search',
      () async {
    final adapter = _LookupAdapter(books: const []);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellBookSearchProvider.notifier);
    notifier.updateSearch('meditations');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    final state = container.read(shellBookSearchProvider);
    expect(state.error, isNull);
    expect(state.searched, true);
    expect(state.results, isEmpty);
  });

  test('two results is a successful, populated search', () async {
    final adapter = _LookupAdapter(books: _twoBooks);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final notifier = container.read(shellBookSearchProvider.notifier);
    notifier.updateSearch('meditations');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    final state = container.read(shellBookSearchProvider);
    expect(state.error, isNull);
    expect(state.results.length, 2);
  });

  test(
      'idempotency: running the same failing query twice leaves the same '
      'forbidden state both times', () async {
    final adapter = _LookupAdapter(statusCode: 403);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);
    final notifier = container.read(shellBookSearchProvider.notifier);

    notifier.updateSearch('flock');
    await Future<void>.delayed(const Duration(milliseconds: 500));
    expect(
      container.read(shellBookSearchProvider).error,
      BookSearchError.forbidden,
    );

    notifier.updateSearch('flock');
    await Future<void>.delayed(const Duration(milliseconds: 500));
    expect(
      container.read(shellBookSearchProvider).error,
      BookSearchError.forbidden,
    );
  });

  test(
      'supersede: an abandoned keystroke\'s failure never lands over a '
      'later successful result', () async {
    final adapter = _LookupAdapter(
      termStatusCode: const {'a': 403},
      termBooks: {'ab': _twoBooks},
    );
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);
    final notifier = container.read(shellBookSearchProvider.notifier);

    notifier.updateSearch('a');
    notifier.updateSearch('ab');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    final state = container.read(shellBookSearchProvider);
    expect(state.error, isNull);
    expect(state.results.length, 2);
  });

  test('an empty query resets to a fresh state and fires no request', () async {
    final adapter = _LookupAdapter(books: _twoBooks);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);
    final notifier = container.read(shellBookSearchProvider.notifier);

    notifier.updateSearch('');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    expect(
      container.read(shellBookSearchProvider),
      const ShellBookSearchState(),
    );
    expect(adapter.lookupRequests, 0);
  });

  test(
      'a malformed lookup payload is distinguishable from a transport '
      'failure in a debug build', () async {
    // A string in place of the int `id` field makes ChaptarrBook.fromJson's
    // `json['id'] as int?` cast throw a TypeError — a non-Dio exception
    // caught by the generic `catch` clause, distinct from a DioException.
    final malformedBooks = [
      {
        'id': 'not-an-int',
        'title': 'Meditations',
        'foreignBookId': 'book-1',
      },
    ];
    final adapter = _LookupAdapter(books: malformedBooks);
    final container = await _makeContainer(adapter: adapter);
    addTearDown(container.dispose);

    final captured = <String>[];
    final originalDebugPrint = debugPrint;
    debugPrint = (String? message, {int? wrapWidth}) {
      if (message != null) captured.add(message);
    };
    addTearDown(() => debugPrint = originalDebugPrint);

    final notifier = container.read(shellBookSearchProvider.notifier);
    notifier.updateSearch('meditations');
    await Future<void>.delayed(const Duration(milliseconds: 500));

    expect(
      container.read(shellBookSearchProvider).error,
      BookSearchError.requestFailed,
      reason: 'IN-02: the user-facing state is unchanged by the diagnostic',
    );

    final output = captured.join('\n');
    expect(
      output,
      contains('TypeError'),
      reason: 'T-03-12/IN-02: the parse failure is now nameable in a debug '
          'build via the caught object\'s runtimeType (TypeError, from the '
          "malformed 'id' field's failed cast) — distinguishable from a "
          'transport failure, which would instead name a DioException',
    );
    expect(
      output,
      isNot(contains('http')),
      reason: 'T-03-12: the diagnostic must never emit a host or URL',
    );
    expect(
      output,
      isNot(contains('books')),
      reason: 'T-03-12: the diagnostic must never emit the active instance '
          'id used by this harness',
    );
    expect(
      output,
      isNot(contains('meditations')),
      reason: 'T-03-12: the diagnostic must never emit the search term',
    );
  });
}

const _books = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true),
    instances: [
      ServiceInstance(
        id: 'books',
        serviceType: 'chaptarr',
        name: 'Books',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

/// FAIL-01 fixture: the grant is present (the route stays reachable) but no
/// Chaptarr instance is configured.
const _noInstanceState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(chaptarr: true),
    instances: [],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

final _twoBooks = [
  {
    'title': 'Meditations',
    'foreignBookId': 'book-1',
    'year': 2002,
    'author': {'id': 0, 'authorName': 'Marcus Aurelius'},
  },
  {
    'title': 'Letters from a Stoic',
    'foreignBookId': 'book-2',
    'year': 1965,
    'author': {'id': 0, 'authorName': 'Seneca'},
  },
];

/// instanceProvider (which the notifier reads) derives from authProvider's
/// resolved value, so every caller awaits authProvider.future before driving
/// the notifier — otherwise a still-AsyncLoading auth state would read as no
/// configured instances regardless of [authState].
Future<ProviderContainer> _makeContainer({
  AuthState? authState,
  required _LookupAdapter adapter,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = adapter;
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(authState ?? _books)),
      backendClientProvider.overrideWithValue(dio),
    ],
  );
  await container.read(authProvider.future);
  return container;
}

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

/// Fakes `GET .../book/lookup`, either uniformly (via [statusCode]/[books])
/// or per search [term] (via [termStatusCode]/[termBooks]) — the latter is
/// what lets the supersede case answer two different queries differently on
/// the same notifier instance.
class _LookupAdapter implements HttpClientAdapter {
  _LookupAdapter({
    this.statusCode = 200,
    this.books = const [],
    this.termStatusCode,
    this.termBooks,
  });

  final int statusCode;
  final List<Map<String, dynamic>> books;
  final Map<String, int>? termStatusCode;
  final Map<String, List<Map<String, dynamic>>>? termBooks;

  int lookupRequests = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (!options.path.endsWith('/book/lookup')) {
      return ResponseBody.fromString(
        '{}',
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    lookupRequests++;
    final term = options.queryParameters['term']?.toString() ?? '';
    final code = termStatusCode?[term] ?? statusCode;
    final body = termBooks?[term] ?? books;
    return ResponseBody.fromString(
      jsonEncode(body),
      code,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
