import 'dart:convert';

import 'package:cantinarr/features/auth/logic/saved_servers_provider.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  ProviderContainer container() {
    final result = ProviderContainer();
    addTearDown(result.dispose);
    return result;
  }

  SavedServer server(String url, [String? name]) =>
      SavedServer.fromAddress(url, name: name)!;

  test('successful visits deduplicate, update names, and persist recency',
      () async {
    final first = container();
    final history = first.read(savedServersProvider.notifier);
    await history.remember(server('http://home.local:8585/', 'Home'));
    await history.remember(server('https://remote.example/library', 'Remote'));
    await history.remember(server(' HTTP://home.local:8585// ', 'Renamed'));
    final restarted = await container().read(savedServersProvider.future);
    expect(restarted.map((s) => s.url), [
      'http://home.local:8585',
      'https://remote.example/library',
    ]);
    expect(restarted.first.name, 'Renamed');
    final prefs = await SharedPreferences.getInstance();
    expect(
        jsonDecode(prefs.getString(SavedServersNotifier.storageKey)!),
        [
          {'url': 'http://home.local:8585', 'name': 'Renamed'},
          {'url': 'https://remote.example/library', 'name': 'Remote'},
        ],
        reason: 'shortcuts persist only the address and optional server name');
  });

  test('schemes, ports, and case-sensitive base paths stay distinct', () async {
    final history = container().read(savedServersProvider.notifier);
    final urls = [
      'http://example.test',
      'https://example.test',
      'https://example.test:8585',
      'https://example.test/Library',
      'https://example.test/library',
    ];
    await Future.wait(urls.map((url) => history.remember(server(url))));
    expect((await history.future).map((s) => s.url), urls.reversed,
        reason: 'overlapping updates must not lose a successful visit');
  });

  test('migration seeds once and never resurrects a forgotten server',
      () async {
    final history = container().read(savedServersProvider.notifier);
    final home = server('http://home.local', 'Home');
    await history.migrateLegacySession(home);
    expect((await history.future).single.name, 'Home');
    await history.forget(home.url);
    final restarted = container().read(savedServersProvider.notifier);
    await restarted.migrateLegacySession(home);
    expect(await restarted.future, isEmpty);
  });

  test('forget default selects next remaining; undo preserves newer visits',
      () async {
    final history = container().read(savedServersProvider.notifier);
    final a = server('https://a.test'), b = server('https://b.test');
    await history.remember(b);
    await history.remember(a);
    final previous = await history.future;
    await history.forget(a.url);
    expect((await history.future).first.url, b.url);
    await history.remember(server('https://new.test'));
    await history.undoForget(a, previous);
    expect((await history.future).map((s) => s.url),
        ['https://new.test', a.url, b.url]);
    await history.undoForget(a, previous);
    expect((await history.future).length, 3);
  });

  test('bad saved entries do not prevent recovery of valid shortcuts',
      () async {
    SharedPreferences.setMockInitialValues({
      SavedServersNotifier.storageKey: jsonEncode([
        null,
        {'url': 42},
        {'url': 'https://good.test', 'name': '  Home  '},
        {'url': 'https://good.test/'},
        {'url': 'https://user:secret@bad.test'},
        {'url': 'https://bad.test?token=secret'},
        {'url': 'https://bad.test#secret'},
        {'url': '  '},
      ]),
    });
    final saved = await container().read(savedServersProvider.future);
    expect(saved.map((s) => s.url), ['https://good.test']);
    expect(saved.single.name, 'Home');
    expect(SavedServer.fromAddress('https://good.test', name: ' ')!.label,
        'https://good.test');
  });

  test('malformed history is recoverable and clearing app data resets it',
      () async {
    SharedPreferences.setMockInitialValues({
      SavedServersNotifier.storageKey: '{broken',
    });
    final history = container().read(savedServersProvider.notifier);
    expect(await history.future, isEmpty);
    await history.remember(server('https://home.test'));
    expect((await history.future).length, 1);
    SharedPreferences.setMockInitialValues({});
    expect(await container().read(savedServersProvider.future), isEmpty);
  });
}
