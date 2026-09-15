import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/media_access/data/listening_apps.dart';
import 'package:cantinarr/features/media_access/data/media_access_service.dart';
import 'package:cantinarr/features/media_access/logic/listening_apps_provider.dart';
import 'package:cantinarr/features/settings/ui/listening_apps_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _Auth extends AuthNotifier {
  _Auth(this.role);
  final String role;
  @override
  Future<AuthState> build() async => AuthState(
        connection: const BackendConnection(
            serverUrl: 'https://cantinarr.example',
            accessToken: 'access',
            refreshToken: 'refresh'),
        user: UserProfile(id: 1, username: 'reader', role: role),
      );

  void switchAccount(
      {int userId = 1, String server = 'https://cantinarr.example'}) {
    state = AsyncData(AuthState(
      connection: BackendConnection(
          serverUrl: server, accessToken: 'access', refreshToken: 'refresh'),
      user: UserProfile(id: userId, username: 'reader', role: role),
    ));
  }
}

class _Service extends MediaAccessService {
  _Service() : super(backendDio: Dio());
  ListeningApps saved = const ListeningApps();
  bool failRead = false, failSave = false;
  @override
  Future<ListeningApps> getListeningAppPreferences() async {
    if (failRead) throw StateError('unavailable');
    return saved;
  }

  @override
  Future<ListeningApps> saveListeningAppPreferences(ListeningApps apps) async {
    if (failSave) throw StateError('unavailable');
    return saved = apps;
  }
}

Future<void> _pump(WidgetTester tester, _Service service,
    {String role = 'user',
    double scale = 1,
    double width = 800,
    _Auth? auth}) async {
  tester.view.physicalSize = Size(width, 1800);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(ProviderScope(
      overrides: [
        authProvider.overrideWith(() => auth ?? _Auth(role)),
        mediaAccessServiceProvider.overrideWithValue(service),
      ],
      child: MaterialApp(
          theme: AppTheme.dark,
          builder: (context, child) => MediaQuery(
              data: MediaQuery.of(context)
                  .copyWith(textScaler: TextScaler.linear(scale)),
              child: child!),
          home: const ListeningAppsScreen())));
  await tester.pumpAndSettle();
}

Future<void> _choose(WidgetTester tester, String platform, String app) async {
  await tester
      .tap(find.widgetWithText(DropdownButtonFormField<String>, platform));
  await tester.pumpAndSettle();
  await tester.tap(find.text(app).last);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('switching user or Cantinarr server drops the previous choices',
      (tester) async {
    final auth = _Auth('user');
    final service = _Service()..saved = const ListeningApps(ios: 'shelfplayer');
    await _pump(tester, service, auth: auth);
    expect(find.text('ShelfPlayer'), findsOneWidget);

    service.saved = const ListeningApps(android: 'theshelf');
    auth.switchAccount(userId: 2);
    await tester.pumpAndSettle();
    expect(find.text('ShelfPlayer'), findsNothing);
    expect(find.text('TheShelf'), findsOneWidget);

    service.saved = const ListeningApps(ios: 'browser', android: 'browser');
    auth.switchAccount(userId: 2, server: 'https://other.example');
    await tester.pumpAndSettle();
    expect(find.text('TheShelf'), findsNothing);
    expect(find.text('Browser'), findsNWidgets(2));
  });

  for (final role in ['admin', 'user']) {
    testWidgets(
        '$role can save both platform choices and return to the admin default',
        (tester) async {
      final service = _Service();
      await _pump(tester, service, role: role);
      expect(find.text('Use admin default'), findsNWidgets(2));
      await _choose(tester, 'iPhone and iPad', 'ShelfPlayer');
      expect(service.saved.ios, 'shelfplayer');
      await _choose(tester, 'Android', 'TheShelf');
      expect(service.saved.android, 'theshelf');
      expect(service.saved.ios, 'shelfplayer');
      await _choose(tester, 'iPhone and iPad', 'Browser');
      expect(service.saved.ios, 'browser');
      await _choose(tester, 'Android', 'Use admin default');
      expect(service.saved.android, '');
      final container = ProviderScope.containerOf(
          tester.element(find.byType(ListeningAppsScreen)));
      container.invalidate(listeningAppPreferencesProvider);
      await tester.pumpAndSettle();
      expect(find.text('Browser'), findsOneWidget);
      expect(find.text('Use admin default'), findsOneWidget);
    });
  }

  testWidgets('a rejected save restores the saved selection', (tester) async {
    final service = _Service()..failSave = true;
    await _pump(tester, service);
    await _choose(tester, 'iPhone and iPad', 'ShelfPlayer');
    expect(service.saved.ios, '');
    expect(find.text('Use admin default'), findsNWidgets(2));
    expect(find.text("Couldn't save your listening apps. Try again."),
        findsOneWidget);
  });

  testWidgets(
      'a failed read does not present guessed preferences and can retry',
      (tester) async {
    final service = _Service()..failRead = true;
    await _pump(tester, service);
    expect(find.byType(DropdownButtonFormField<String>), findsNothing);
    service.failRead = false;
    await tester.tap(find.text("Couldn't load listening apps · Retry"));
    await tester.pumpAndSettle();
    expect(find.text('Use admin default'), findsNWidgets(2));
  });

  testWidgets('the choices fit narrow screens with larger text',
      (tester) async {
    await _pump(tester, _Service(), width: 320, scale: 2);
    await _choose(tester, 'iPhone and iPad', 'ShelfPlayer');
    expect(tester.takeException(), isNull);
  });
}
