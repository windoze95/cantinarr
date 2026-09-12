import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/apple_tv/data/apple_tv_service.dart';
import 'package:cantinarr/features/apple_tv/ui/apple_tv_open_button.dart';
import 'package:cantinarr/features/apple_tv/ui/apple_tvs_screen.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _tv = AppleTV(id: 'tv-1', name: 'Living room', address: '192.0.2.10', identifier: 'stable');
const _other = AppleTV(id: 'tv-2', name: 'Den');

class _Service extends AppleTVService {
  _Service() : super(Dio());
  List<AppleTV> devices = [_tv];
  final calls = <String>[];
  bool failList = false;
  bool failOpen = false;
  @override
  Future<AppleTVList> list() async {
    calls.add('list');
    if (failList) throw Exception('private-host');
    return AppleTVList(supported: true, devices: devices);
  }
  @override
  Future<List<AppleTV>> discover(String address) async { calls.add('discover:$address'); return devices; }
  @override
  Future<String> begin(AppleTV tv) async { calls.add('pair:${tv.id}'); return 'pairing-id'; }
  @override
  Future<void> cancel(String id) async { calls.add('cancel:$id'); }
  @override
  Future<void> complete(String id, String pin) async {
    expect(pin, '2468'); calls.add('complete:$id');
  }
  @override
  Future<AppleTVHandoff> open(String id, String type, int tmdbId) async {
    calls.add('open:$id:$type:$tmdbId');
    if (failOpen) throw Exception('private-host');
    return AppleTVHandoff(id: 'one-use', expiresAt: DateTime.now().add(const Duration(seconds: 30)));
  }
  @override
  Future<void> confirm(String id, String confirmationId) async { calls.add('confirm:$id:$confirmationId'); }
}

class _Auth extends AuthNotifier {
  _Auth(this.initial);
  final AuthState initial;
  @override
  Future<AuthState> build() async => initial;
}

Future<void> _pump(WidgetTester tester, _Service service, {bool child = false,
    bool capable = true, bool admin = false, bool setup = false, TargetPlatform platform = TargetPlatform.android}) async {
  await tester.pumpWidget(ProviderScope(overrides: [
    appleTVServiceProvider.overrideWithValue(service),
    authProvider.overrideWith(() => _Auth(AuthState(
      connection: BackendConnection(serverUrl: 'http://localhost', accessToken: 'test',
        refreshToken: 'test', appleTvRemote: capable),
      user: UserProfile(id: 1, username: 'viewer', role: admin ? 'admin' : 'user', child: child),
    ))),
  ], child: MaterialApp(theme: ThemeData(platform: platform), home: setup
    ? const AppleTVsScreen()
    : const Scaffold(body: AppleTVOpenButton(mediaType: MediaType.tv, tmdbId: 12)),
  )));
  await tester.pumpAndSettle();
}

void main() {
  test('optional config capability defaults off and survives token refresh', () {
    expect(ServerConfig.fromJson({}).appleTvRemote, isFalse);
    expect(ServerConfig.fromJson({'apple_tv_remote': true}).appleTvRemote, isTrue);
    const connection = BackendConnection(serverUrl: '', accessToken: '', refreshToken: '', appleTvRemote: true);
    expect(connection.copyWith(accessToken: 'refreshed').appleTvRemote, isTrue);
    expect(connection.copyWith(appleTvRemote: false).appleTvRemote, isFalse);
  });

  for (final platform in [TargetPlatform.android, TargetPlatform.iOS]) {
    testWidgets('$platform opens chosen TV without automatically confirming', (tester) async {
      final service = _Service();
      await _pump(tester, service, platform: platform);
      await tester.tap(find.text('Open on Living room'));
      await tester.pumpAndSettle();
      expect(service.calls.where((c) => c.startsWith('open:')), ['open:tv-1:tv:12']);
      expect(service.calls.where((c) => c.startsWith('confirm:')), isEmpty);
      expect(find.text('Sent to Living room'), findsOneWidget);
      await tester.tap(find.text('Confirm Open'));
      await tester.pumpAndSettle();
      expect(service.calls.where((c) => c.startsWith('confirm:')), ['confirm:tv-1:one-use']);
    });
  }

  testWidgets('Done does not send a Select that might start playback', (tester) async {
    final service = _Service();
    await _pump(tester, service);
    await tester.tap(find.text('Open on Living room')); await tester.pumpAndSettle();
    await tester.tap(find.text('Done')); await tester.pumpAndSettle();
    expect(service.calls.where((c) => c.startsWith('confirm:')), isEmpty);
  });

  testWidgets('multiple TVs require a destination choice', (tester) async {
    final service = _Service()..devices = [_tv, _other];
    await _pump(tester, service);
    await tester.tap(find.text('Open on Apple TV')); await tester.pumpAndSettle();
    expect(service.calls.where((c) => c.startsWith('open:')), isEmpty);
    await tester.tap(find.text('Den')); await tester.pumpAndSettle();
    expect(service.calls, contains('open:tv-2:tv:12'));
    await tester.tap(find.text('Done')); await tester.pumpAndSettle();
  });

  testWidgets('kids and old servers never query TV devices', (tester) async {
    final service = _Service();
    await _pump(tester, service, child: true);
    expect(find.byType(TextButton), findsNothing);
    expect(service.calls, isEmpty);
    await tester.pumpWidget(const SizedBox());
    await _pump(tester, service, capable: false);
    expect(find.byType(TextButton), findsNothing);
    expect(service.calls, isEmpty);
  });

  testWidgets('a failed list is distinguishable from no granted TVs', (tester) async {
    final service = _Service()..failList = true;
    await _pump(tester, service);
    expect(find.text('Couldn’t load Apple TVs · Retry'), findsOneWidget);
    service..failList = false..devices = [];
    await tester.tap(find.text('Couldn’t load Apple TVs · Retry')); await tester.pumpAndSettle();
    expect(find.byType(TextButton), findsNothing);
  });

  testWidgets('launch failure shows safe copy without retry or confirmation', (tester) async {
    final service = _Service()..failOpen = true;
    await _pump(tester, service);
    await tester.tap(find.text('Open on Living room')); await tester.pumpAndSettle();
    expect(find.textContaining('private-host'), findsNothing);
    expect(find.text('Confirm Open'), findsNothing);
    expect(service.calls.where((c) => c.startsWith('open:')).length, 1);
  });

  testWidgets('admin discovery pairs with a masked PIN and cancels explicitly', (tester) async {
    final service = _Service();
    await _pump(tester, service, admin: true, setup: true);
    await tester.tap(find.text('Add Apple TV')); await tester.pumpAndSettle();
    await tester.tap(find.text('Search for TVs')); await tester.pumpAndSettle();
    await tester.tap(find.text('Pair')); await tester.pumpAndSettle();
    final pin = find.widgetWithText(TextField, 'TV PIN');
    expect(tester.widget<TextField>(pin).obscureText, isTrue);
    await tester.tap(find.text('Cancel')); await tester.pumpAndSettle();
    expect(service.calls, contains('cancel:pairing-id'));
    await tester.tap(find.text('Pair')); await tester.pumpAndSettle();
    await tester.enterText(find.widgetWithText(TextField, 'TV PIN'), '2468');
    await tester.tap(find.text('Pair TV')); await tester.pumpAndSettle();
    expect(service.calls, contains('complete:pairing-id'));
    expect(find.text('TV PIN'), findsNothing);
  });

  test('service sends identifiers only through the backend', () async {
    final requests = <RequestOptions>[];
    final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'));
    dio.interceptors.add(InterceptorsWrapper(onRequest: (options, handler) {
      requests.add(options);
      handler.resolve(Response(requestOptions: options, statusCode: 200,
        data: {'state': 'sent', 'confirmation_id': 'one-use',
          'confirmation_expires_at': '2026-09-12T12:00:00Z'}));
    }));
    final service = AppleTVService(dio);
    final handoff = await service.open('tv-1', 'movie', 34);
    expect(requests.single.uri.toString(), 'https://cantinarr.example/api/apple-tvs/tv-1/open');
    expect(requests.single.data, {'media_type': 'movie', 'tmdb_id': 34});
    await service.confirm('tv-1', handoff.id);
    expect(requests.last.data, {'confirmation_id': 'one-use'});
  });
}
