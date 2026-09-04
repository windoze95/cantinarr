import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/user_request_settings_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// The per-user admin screen: the default-instance dropdowns keep their pin
/// semantics, and the new grants checkboxes (shown only where siblings exist)
/// save additive library access without touching the default.
void main() {
  late _FakeAdapter adapter;

  Future<void> pumpScreen(WidgetTester tester,
      {bool targetIsAdmin = false}) async {
    tester.view.physicalSize = const Size(390, 1600);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = adapter;
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(_adminState())),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: MaterialApp(
          home: UserRequestSettingsScreen(
              userId: 7, username: 'alice', targetIsAdmin: targetIsAdmin),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<void> scrollTo(WidgetTester tester, Finder finder) async {
    await tester.scrollUntilVisible(finder, 200,
        scrollable: find.byType(Scrollable).first);
    await tester.pumpAndSettle();
  }

  Future<void> save(WidgetTester tester) async {
    await scrollTo(tester, find.widgetWithText(ElevatedButton, 'Save'));
    await tester.tap(find.widgetWithText(ElevatedButton, 'Save'));
    await tester.pumpAndSettle();
  }

  bool requireApprovalIsOn(WidgetTester tester) {
    final chip = tester.widget<ChoiceChip>(find.descendant(
      of: find.ancestor(
          of: find.text('Require approval'), matching: find.byType(Column)),
      matching: find.widgetWithText(ChoiceChip, 'On'),
    ).first);
    return chip.selected;
  }

  testWidgets(
      'grants checkboxes appear beside the default and save additively',
      (tester) async {
    adapter = _FakeAdapter(grants: {
      'radarr': ['radarr-4k'],
    });
    await pumpScreen(tester);

    // The screen loaded past its error state and lists the instance section.
    expect(find.text('Retry'), findsNothing);
    expect(find.text('Require approval'), findsOneWidget);

    // The section sits below the fold of the settings ListView.
    await tester.scrollUntilVisible(
      find.text('Also grant Radarr libraries'),
      200,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.pumpAndSettle();

    // Radarr has a sibling, so its grants section renders (checked from the
    // stored grant); single-instance Sonarr gets no grants section.
    expect(find.text('Also grant Radarr libraries'), findsOneWidget);
    expect(find.text('Also grant Sonarr libraries'), findsNothing);
    final fourK = tester.widget<CheckboxListTile>(
        find.widgetWithText(CheckboxListTile, '4K Movies'));
    expect(fourK.value, isTrue);

    // Grant the main library too, then save.
    await tester.tap(find.widgetWithText(CheckboxListTile, 'Movies'));
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(
      find.widgetWithText(ElevatedButton, 'Save'),
      200,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Save'));
    await tester.pumpAndSettle();

    final putGrants = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/admin/users/7/instance-grants');
    final body = putGrants.body as Map<String, dynamic>;
    expect(List.of(body['radarr'] as List)..sort(),
        ['radarr-4k', 'radarr-main']);
    // Every visible type is named so an emptied set clears server-side.
    expect(body.containsKey('sonarr'), isTrue);
    // The default pin was not touched by granting.
    final putDefaults = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/admin/users/7/default-instances');
    expect((putDefaults.body as Map<String, dynamic>)['radarr'], isNull);
  });

  testWidgets(
      'turning the kids account on pre-sets approval and saves the policy first',
      (tester) async {
    adapter = _FakeAdapter();
    await pumpScreen(tester);

    expect(find.text('Kids account'), findsWidgets);
    expect(requireApprovalIsOn(tester), isFalse);
    expect(find.text('Movies up to'), findsNothing);

    await tester.tap(find.byType(SwitchListTile).first);
    await tester.pumpAndSettle();

    // The limits appear on the region's suggested defaults, and approval
    // flipped to On where the admin can still change it.
    expect(find.text('Movies up to'), findsOneWidget);
    expect(find.text('Shows up to'), findsOneWidget);
    expect(find.text('PG'), findsWidgets);
    expect(find.text('TV-PG'), findsWidgets);
    expect(find.text('Hide unrated titles'), findsOneWidget);
    expect(find.text('Hidden genres'), findsOneWidget);
    expect(requireApprovalIsOn(tester), isTrue);

    // Hide Horror for movies.
    await tester.tap(find.widgetWithText(FilterChip, 'Horror'));
    await tester.pumpAndSettle();

    await save(tester);

    final policyPut = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/admin/users/7/content-policy');
    final body = policyPut.body as Map<String, dynamic>;
    expect(body['max_movie_rating'], 'PG');
    expect(body['max_tv_rating'], 'TV-PG');
    expect(body['rating_region'], 'US');
    expect(body['block_unrated'], isTrue);
    expect(body['blocked_movie_genres'], [27]);
    expect(body['blocked_tv_genres'], isEmpty);

    final settingsPut = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/admin/users/7/request-settings');
    expect((settingsPut.body as Map<String, dynamic>)['require_approval'],
        isTrue);
    expect(adapter.requests.indexOf(policyPut),
        lessThan(adapter.requests.indexOf(settingsPut)),
        reason: 'the policy write carries the refusals, so it goes first');
  });

  testWidgets('turning a stored kids account off sends one DELETE and no PUT',
      (tester) async {
    adapter = _FakeAdapter(policy: {
      'max_movie_rating': 'G',
      'max_tv_rating': 'TV-Y',
      'rating_region': 'US',
      'block_unrated': false,
      'blocked_movie_genres': [27],
      'blocked_tv_genres': [],
    });
    await pumpScreen(tester);

    // Loaded on: the limits render from the stored policy.
    expect(find.text('Movies up to'), findsOneWidget);
    expect(find.text('G'), findsWidgets);
    expect(requireApprovalIsOn(tester), isFalse);

    await tester.tap(find.byType(SwitchListTile).first);
    await tester.pumpAndSettle();
    expect(find.text('Movies up to'), findsNothing);
    // Flipping off leaves the approval choice alone.
    expect(requireApprovalIsOn(tester), isFalse);

    await save(tester);

    expect(
        adapter.requests.where((r) => r.path.endsWith('/content-policy') &&
            r.method == 'DELETE'),
        hasLength(1));
    expect(
        adapter.requests.where(
            (r) => r.path.endsWith('/content-policy') && r.method == 'PUT'),
        isEmpty);
  });

  testWidgets('an untouched kids section makes no policy request on save',
      (tester) async {
    adapter = _FakeAdapter();
    await pumpScreen(tester);
    await save(tester);
    expect(
        adapter.requests.where((r) =>
            r.path.endsWith('/content-policy') && r.method != 'GET'),
        isEmpty);
  });

  testWidgets('a server without kids accounts hides the section',
      (tester) async {
    adapter = _FakeAdapter(certifications: null);
    await pumpScreen(tester);
    expect(find.text('Kids account'), findsNothing);
    expect(find.text('Require approval'), findsOneWidget);
  });

  testWidgets('an admin target never shows the section', (tester) async {
    adapter = _FakeAdapter();
    await pumpScreen(tester, targetIsAdmin: true);
    expect(find.text('Kids account'), findsNothing);
    expect(
        adapter.requests.where((r) => r.path.endsWith('/content-policy')),
        isEmpty);
  });

  testWidgets(
      'a ratings list that cannot be read keeps the screen and disables the switch',
      (tester) async {
    adapter = _FakeAdapter(certificationsStatus: 503);
    await pumpScreen(tester);
    expect(find.text('Kids account'), findsWidgets);
    expect(find.text('Require approval'), findsOneWidget);
    expect(find.textContaining("Couldn't load the ratings list"), findsNothing);
    final tile = tester.widget<SwitchListTile>(find.byType(SwitchListTile).first);
    expect(tile.onChanged, isNull,
        reason: 'turning on needs a scheme to pick a cap from');
  });

  testWidgets('changing the region re-defaults both caps', (tester) async {
    adapter = _FakeAdapter(policy: {
      'max_movie_rating': 'PG-13',
      'max_tv_rating': 'TV-14',
      'rating_region': 'US',
      'block_unrated': true,
      'blocked_movie_genres': [],
      'blocked_tv_genres': [],
    });
    await pumpScreen(tester);
    expect(find.text('PG-13'), findsWidgets);

    await tester.tap(find.text('United States'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('United Kingdom').last);
    await tester.pumpAndSettle();

    // GB has no marked default: the second-lowest entry of each scheme.
    expect(find.text('PG-13'), findsNothing);
    expect(find.text('PG'), findsWidgets);
    await save(tester);
    final policyPut = adapter.requests.singleWhere((r) =>
        r.method == 'PUT' && r.path == '/api/admin/users/7/content-policy');
    final body = policyPut.body as Map<String, dynamic>;
    expect(body['rating_region'], 'GB');
    expect(body['max_movie_rating'], 'PG');
    expect(body['max_tv_rating'], 'PG');
  });
}

AuthState _adminState() => const AuthState(
      connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        services: AvailableServices(),
        instances: [
          ServiceInstance(
            id: 'radarr-main',
            serviceType: 'radarr',
            name: 'Movies',
            isDefault: true,
          ),
          ServiceInstance(
            id: 'radarr-4k',
            serviceType: 'radarr',
            name: '4K Movies',
          ),
          ServiceInstance(
            id: 'sonarr-main',
            serviceType: 'sonarr',
            name: 'TV',
            isDefault: true,
          ),
        ],
      ),
      user: UserProfile(id: 1, username: 'admin', role: 'admin'),
    );

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;
  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

class _FakeAdapter implements HttpClientAdapter {
  final Map<String, List<String>> grants;
  final List<({String method, String path, dynamic body})> requests = [];

  /// The user's stored kids-account policy; null answers 404 (not a kids
  /// account), which is what the screen must read as "switch off".
  final Map<String, dynamic>? policy;

  /// The certification schemes; null answers 404 (an older server without
  /// kids accounts), which hides the section. [certificationsStatus]
  /// overrides the status for a failure that is not absence.
  final Map<String, dynamic>? certifications;
  final int? certificationsStatus;

  _FakeAdapter({
    this.grants = const {},
    this.policy,
    this.certifications = _usCatalog,
    this.certificationsStatus,
  });

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

    Object response = <String, dynamic>{};
    var status = 200;
    if (path == '/api/admin/certifications') {
      if (certificationsStatus != null) {
        status = certificationsStatus!;
        response = {'error': 'ratings lists are temporarily unavailable'};
      } else if (certifications == null) {
        status = 404;
        response = {'error': 'not found'};
      } else {
        response = certifications!;
      }
    } else if (path.endsWith('/content-policy')) {
      if (options.method == 'GET') {
        if (policy == null) {
          status = 404;
          response = {'error': 'not a kids account'};
        } else {
          response = policy!;
        }
      } else if (options.method == 'PUT') {
        response = body as Map<String, dynamic>;
      } else {
        response = {'status': 'cleared'};
      }
    } else if (path == '/api/genres/movie') {
      response = {
        'genres': [
          {'id': 18, 'name': 'Drama'},
          {'id': 27, 'name': 'Horror'},
        ],
      };
    } else if (path == '/api/genres/tv') {
      response = {
        'genres': [
          {'id': 18, 'name': 'Drama'},
          {'id': 10768, 'name': 'War & Politics'},
        ],
      };
    } else if (path == '/api/providers/regions') {
      response = {
        'results': [
          {'iso_3166_1': 'US', 'english_name': 'United States'},
          {'iso_3166_1': 'GB', 'english_name': 'United Kingdom'},
        ],
      };
    } else if (path.endsWith('/request-settings') && options.method == 'GET') {
      response = path.contains('/users/')
          ? <String, dynamic>{}
          : {
              'settings': {
                'require_approval': false,
                'allow_season_choice': true,
                'default_season_scope': 'all',
                'allow_quality_choice': false,
              },
              'radarr_profiles': <dynamic>[],
              'sonarr_profiles': <dynamic>[],
            };
    } else if (path.endsWith('/default-instances')) {
      response = <String, dynamic>{};
    } else if (path.endsWith('/instance-grants')) {
      response = grants;
    }
    return ResponseBody.fromString(
      jsonEncode(response),
      status,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

const _usCatalog = <String, dynamic>{
  'movie': {
    'US': [
      {'certification': 'NR', 'order': 0},
      {'certification': 'G', 'order': 1, 'meaning': 'All ages admitted.'},
      {'certification': 'PG', 'order': 2, 'default': true},
      {'certification': 'PG-13', 'order': 3},
      {'certification': 'R', 'order': 4},
    ],
    'GB': [
      {'certification': 'U', 'order': 1},
      {'certification': 'PG', 'order': 2},
      {'certification': '12A', 'order': 3},
      {'certification': '15', 'order': 4},
    ],
  },
  'tv': {
    'US': [
      {'certification': 'NR', 'order': 0},
      {'certification': 'TV-Y', 'order': 1},
      {'certification': 'TV-Y7', 'order': 2},
      {'certification': 'TV-G', 'order': 3},
      {'certification': 'TV-PG', 'order': 4, 'default': true},
      {'certification': 'TV-14', 'order': 5},
      {'certification': 'TV-MA', 'order': 6},
    ],
    'GB': [
      {'certification': 'U', 'order': 1},
      {'certification': 'PG', 'order': 2},
      {'certification': '12', 'order': 3},
    ],
  },
  'source': 'tmdb',
};
