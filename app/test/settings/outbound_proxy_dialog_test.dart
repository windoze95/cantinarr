import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/ai_assistant/data/ai_settings_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/settings_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// The Outbound Proxy tile and its editor on the root settings screen: the
/// tile shows the configured address, the dialog pre-fills what the server
/// stores (never the password), Test reports the server's verdict inline and
/// retires it on any edit, and Save sends exactly what was typed.

const _configured = {
  'url': 'http://proxy:8118',
  'username': 'vpn',
  'has_password': true,
};

const _unset = {'url': '', 'username': '', 'has_password': false};

const _fallback = 'Route internet traffic through a proxy';
const _success = 'Proxy works: TMDB reached through it.';

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    PackageInfo.setMockInitialValues(
      appName: 'Cantinarr',
      packageName: 'com.example.cantinarr',
      version: '1.0.0',
      buildNumber: '1',
      buildSignature: '',
    );
  });

  testWidgets('the tile shows the configured address', (tester) async {
    await _pump(tester, _FakeAdapter(proxy: _configured));
    await _revealTile(tester);

    expect(find.text('http://proxy:8118'), findsOneWidget);
    expect(find.text(_fallback), findsNothing);
  });

  testWidgets('the tile describes itself while no proxy is set',
      (tester) async {
    await _pump(tester, _FakeAdapter(proxy: _unset));
    await _revealTile(tester);

    expect(find.text(_fallback), findsOneWidget);
  });

  testWidgets('the dialog pre-fills the address and username, never the '
      'password', (tester) async {
    await _pump(tester, _FakeAdapter(proxy: _configured));
    await _openDialog(tester);

    expect(find.byType(AlertDialog), findsOneWidget);
    expect(_fieldText(tester, 'Proxy address'), 'http://proxy:8118');
    expect(_fieldText(tester, 'Username (optional)'), 'vpn');
    expect(_fieldText(tester, 'Password'), '');
    expect(find.text('Leave blank to keep the saved password'), findsOneWidget);
  });

  testWidgets('the password helper only appears once a password is saved',
      (tester) async {
    await _pump(tester, _FakeAdapter(proxy: _unset));
    await _openDialog(tester);

    expect(_fieldText(tester, 'Proxy address'), '');
    expect(find.text('Leave blank to keep the saved password'), findsNothing);
  });

  testWidgets('Test reports success inline and sends what is typed',
      (tester) async {
    final adapter = _FakeAdapter(proxy: _configured);
    await _pump(tester, adapter);
    await _openDialog(tester);

    await tester.tap(_testButton());
    await tester.pumpAndSettle();

    expect(find.text(_success), findsOneWidget);
    final test = adapter.requests.singleWhere((r) => r.method == 'POST');
    expect(test.path, '/api/admin/outbound-proxy/test');
    expect(test.body, {
      'url': 'http://proxy:8118',
      'username': 'vpn',
      'password': '',
    });
    // Testing stores nothing.
    expect(adapter.requests.where((r) => r.method == 'PUT'), isEmpty);
    expect(find.byType(AlertDialog), findsOneWidget);
  });

  testWidgets("Test shows the server's reason when the proxy fails",
      (tester) async {
    const reason =
        'proxy test failed: dial tcp 10.0.0.5:8118: connect: connection refused';
    await _pump(
      tester,
      _FakeAdapter(proxy: _configured, testError: reason),
    );
    await _openDialog(tester);

    await tester.tap(_testButton());
    await tester.pumpAndSettle();

    expect(find.text(reason), findsOneWidget);
    expect(find.text(_success), findsNothing);
    expect(
      tester.widget<Text>(find.text(reason)).style?.color,
      AppTheme.error,
    );
  });

  testWidgets('editing any field retires the last result', (tester) async {
    await _pump(tester, _FakeAdapter(proxy: _configured));
    await _openDialog(tester);

    await tester.tap(_testButton());
    await tester.pumpAndSettle();
    expect(find.text(_success), findsOneWidget);

    await tester.enterText(_field('Proxy address'), 'http://proxy:8119');
    await tester.pumpAndSettle();
    expect(find.text(_success), findsNothing);

    // A fresh pass for the new address, then a credential edit retires it.
    await tester.tap(_testButton());
    await tester.pumpAndSettle();
    expect(find.text(_success), findsOneWidget);
    await tester.enterText(_field('Password'), 'hunter2');
    await tester.pumpAndSettle();
    expect(find.text(_success), findsNothing);
  });

  testWidgets('Save sends the typed values and the tile follows the reply',
      (tester) async {
    final adapter = _FakeAdapter(proxy: _unset);
    await _pump(tester, adapter);
    await _openDialog(tester);

    await tester.enterText(_field('Proxy address'), 'socks5h://10.0.0.5:1080');
    await tester.enterText(_field('Username (optional)'), 'me');
    await tester.enterText(_field('Password'), 'hunter2');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Save'));
    await tester.pumpAndSettle();

    final put = adapter.requests.singleWhere((r) => r.method == 'PUT');
    expect(put.path, '/api/admin/outbound-proxy');
    expect(put.body, {
      'url': 'socks5h://10.0.0.5:1080',
      'username': 'me',
      'password': 'hunter2',
    });
    expect(find.byType(AlertDialog), findsNothing);
    expect(find.text('socks5h://10.0.0.5:1080'), findsOneWidget);
    expect(find.text(_fallback), findsNothing);

    // Reopening reflects the stored state, password still withheld.
    await _openDialog(tester);
    expect(_fieldText(tester, 'Proxy address'), 'socks5h://10.0.0.5:1080');
    expect(_fieldText(tester, 'Username (optional)'), 'me');
    expect(_fieldText(tester, 'Password'), '');
    expect(find.text('Leave blank to keep the saved password'), findsOneWidget);
  });

  testWidgets('a rejected save keeps the dialog open and says why',
      (tester) async {
    const reason = 'proxy url must be scheme://host:port';
    await _pump(
      tester,
      _FakeAdapter(proxy: _unset, saveError: reason),
    );
    await _openDialog(tester);

    await tester.enterText(_field('Proxy address'), 'proxy:8118');
    await tester.tap(find.widgetWithText(ElevatedButton, 'Save'));
    await tester.pumpAndSettle();

    expect(find.byType(AlertDialog), findsOneWidget);
    expect(find.text('Failed to save: $reason'), findsOneWidget);
    expect(find.text(_fallback), findsOneWidget);
  });

  testWidgets('Test is disabled while the address is blank', (tester) async {
    await _pump(tester, _FakeAdapter(proxy: _unset));
    await _openDialog(tester);

    expect(tester.widget<TextButton>(_testButton()).enabled, isFalse);

    await tester.enterText(_field('Proxy address'), 'http://proxy:8118');
    await tester.pumpAndSettle();
    expect(tester.widget<TextButton>(_testButton()).enabled, isTrue);

    await tester.enterText(_field('Proxy address'), '   ');
    await tester.pumpAndSettle();
    expect(tester.widget<TextButton>(_testButton()).enabled, isFalse);
  });
}

Finder _testButton() => find.widgetWithText(TextButton, 'Test');

Finder _field(String label) => find.byWidgetPredicate(
      (w) => w is TextField && w.decoration?.labelText == label,
    );

String _fieldText(WidgetTester tester, String label) =>
    tester.widget<TextField>(_field(label)).controller!.text;

/// Scrolls the Admin section into view; the tile sits below the fold.
Future<void> _revealTile(WidgetTester tester) async {
  final tile = find.text('Outbound Proxy');
  final scrollable = find.byType(ListView).first;
  for (var i = 0; i < 40 && tile.evaluate().isEmpty; i++) {
    await tester.drag(scrollable, const Offset(0, -80));
    await tester.pumpAndSettle();
  }
  await tester.ensureVisible(tile);
  await tester.pumpAndSettle();
}

Future<void> _openDialog(WidgetTester tester) async {
  await _revealTile(tester);
  await tester.tap(find.text('Outbound Proxy'));
  await tester.pumpAndSettle();
}

Future<void> _pump(WidgetTester tester, _FakeAdapter adapter) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        backendClientProvider.overrideWithValue(dio),
        authProvider.overrideWith(_FakeAuthNotifier.new),
        aiSettingsProvider.overrideWith((_) async => _aiSettings()),
      ],
      child: MaterialApp(theme: AppTheme.dark, home: const SettingsScreen()),
    ),
  );
  await tester.pumpAndSettle();
}

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
        ),
        user: UserProfile(
          id: 1,
          username: 'admin',
          role: 'admin',
          permissions: ['ai:chat'],
        ),
      );

  @override
  Future<void> refreshUser() async {}
}

/// Serves the proxy endpoints the way the server does and records every
/// request (method, path, decoded body) for assertions. Other settings
/// endpoints get empty-but-valid payloads.
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter({required this.proxy, this.testError, this.saveError});

  /// What GET /api/admin/outbound-proxy answers; a successful PUT replaces
  /// it with the server-shaped echo (url, username, whether a password
  /// was sent), so the tile can follow the stored state.
  Map<String, dynamic> proxy;

  /// POST .../test answers 400 with this reason when set, 204 otherwise.
  final String? testError;

  /// PUT answers 400 with this reason when set.
  final String? saveError;

  final List<({String method, String path, dynamic body})> requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic body;
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    final path = options.uri.path;
    requests.add((method: options.method, path: path, body: body));

    if (options.method == 'POST' && path == '/api/admin/outbound-proxy/test') {
      final error = testError;
      if (error != null) return _rejection(error);
      return ResponseBody.fromString('', 204, headers: {});
    }
    if (options.method == 'PUT' && path == '/api/admin/outbound-proxy') {
      final error = saveError;
      if (error != null) return _rejection(error);
      final sent = body as Map<String, dynamic>;
      proxy = {
        'url': sent['url'],
        'username': sent['username'],
        'has_password': (sent['password'] as String).isNotEmpty,
      };
      return _json(proxy);
    }
    return _json(switch (path) {
      '/api/admin/outbound-proxy' => proxy,
      '/api/admin/setup-status' => const {
          'items': <dynamic>[],
          'configured': 0,
          'total': 0,
        },
      '/api/admin/update-status' => const {
          'update': <String, dynamic>{},
          'management_url': '',
        },
      _ => const <String, dynamic>{},
    });
  }

  ResponseBody _json(Map<String, dynamic> payload) => ResponseBody.fromString(
        jsonEncode(payload),
        200,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );

  /// The server's validation shape: a JSON `{error}` body with a 400.
  ResponseBody _rejection(String error) => ResponseBody.fromString(
        '${jsonEncode({'error': error})}\n',
        400,
        headers: {
          'content-type': ['application/json'],
        },
      );

  @override
  void close({bool force = false}) {}
}

AiSettings _aiSettings() => const AiSettings(
      providers: [],
      personal: PersonalAiSettings(
        selected: false,
        config: null,
        credentials: {},
      ),
      shared: SharedAiSettings(
        granted: true,
        configured: true,
        config: AiProviderConfig(provider: 'openai', model: 'gpt-5.5'),
      ),
      effective: EffectiveAiSettings(
        available: true,
        source: AiAccessSource.shared,
        provider: 'openai',
        model: 'gpt-5.5',
        reason: '',
      ),
    );
