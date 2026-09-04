import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/widgets/see_all_button.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/ui/media_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

/// The title page's Cast row, cast and crew sheet, and Details section,
/// fed by the credits append and the production fields of a detail body.
/// Profile paths are null throughout so no image is ever requested, and
/// the cast stays at three so the lazily built row renders every card in
/// a 390px viewport.
void main() {
  testWidgets(
      'the cast row lists the billing in order, and Details reads the known '
      'lines with the studios as chips', (tester) async {
    await _pumpDetail(tester, type: MediaType.movie, body: _matrix);

    expect(find.text('Cast'), findsOneWidget);
    for (final name in ['Keanu Reeves', 'Laurence Fishburne', 'Carrie-Anne Moss']) {
      expect(find.text(name), findsOneWidget);
    }
    for (final role in ['Neo', 'Morpheus', 'Trinity']) {
      expect(find.text(role), findsOneWidget);
    }
    expect(
      tester.getTopLeft(find.text('Keanu Reeves')).dx,
      lessThan(tester.getTopLeft(find.text('Laurence Fishburne')).dx),
    );

    expect(find.text('Details'), findsOneWidget);
    const lines = {
      'Directed by': 'Lana Wachowski, Lilly Wachowski',
      'Written by': 'Lana Wachowski',
      'Country': 'United States of America',
      'Runtime': '2h 16m',
      'Budget': '\$63M',
      'Revenue': '\$464M',
    };
    for (final entry in lines.entries) {
      expect(find.text(entry.key), findsOneWidget, reason: entry.key);
      expect(find.text(entry.value), findsOneWidget, reason: entry.key);
    }
    // A released film carries no status line.
    expect(find.text('Status'), findsNothing);
    expect(find.text('Studio'), findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'Warner Bros. Pictures'),
        findsOneWidget);
    expect(find.widgetWithText(ActionChip, 'Village Roadshow Pictures'),
        findsOneWidget);
  });

  testWidgets(
      'See all on Cast opens the cast and crew sheet grouped by department, '
      'and a crew row hands off to that person\'s sheet', (tester) async {
    await _pumpDetail(tester, type: MediaType.movie, body: _matrix);

    await _tap(tester, _seeAllFor('Cast'));
    expect(find.text('Cast & crew'), findsOneWidget);
    // The billing is listed again inside the sheet.
    expect(find.text('Keanu Reeves'), findsNWidgets(2));
    expect(find.text('Directing'), findsOneWidget);
    expect(find.text('Director'), findsNWidgets(2));
    expect(find.text('Writing'), findsOneWidget);
    expect(find.text('Screenplay'), findsOneWidget);
    // The sheet's list is lazy; the last department sits past its first
    // screen, so scroll the sheet itself to reach it.
    await tester.scrollUntilVisible(
      find.text('Producer'),
      120,
      scrollable: _sheetScrollable(),
    );
    await tester.pumpAndSettle();
    expect(find.text('Production'), findsOneWidget);
    expect(find.text('Producer'), findsOneWidget);

    await tester.tap(find.text('Joel Silver'));
    await tester.pumpAndSettle();
    // The crew sheet is gone; the person sheet is up with his name.
    expect(find.text('Cast & crew'), findsNothing);
    expect(find.byType(DraggableScrollableSheet), findsOneWidget);
    expect(find.text('Joel Silver'), findsOneWidget);
  });

  testWidgets('tapping a cast card opens that person\'s sheet',
      (tester) async {
    await _pumpDetail(tester, type: MediaType.movie, body: _matrix);

    await _tap(tester, find.text('Carrie-Anne Moss'));
    expect(find.byType(DraggableScrollableSheet), findsOneWidget);
    expect(find.text('Carrie-Anne Moss'), findsNWidgets(2));
  });

  testWidgets(
      'a body without credits or production facts renders neither section',
      (tester) async {
    await _pumpDetail(tester, type: MediaType.movie, body: {
      'id': 603,
      'title': 'The Matrix',
      'genres': [
        {'id': 28, 'name': 'Action'},
      ],
    });

    expect(find.text('Cast'), findsNothing);
    expect(find.text('Details'), findsNothing);
    expect(_seeAllFor('Cast'), findsNothing);
  });

  testWidgets('an unreleased movie leads its Details with the status',
      (tester) async {
    await _pumpDetail(tester, type: MediaType.movie, body: {
      'id': 1,
      'title': 'Soon',
      'status': 'Post Production',
      'runtime': 0,
      'budget': 0,
      'revenue': 0,
    });

    expect(find.text('Details'), findsOneWidget);
    expect(find.text('Status'), findsOneWidget);
    expect(find.text('Post Production'), findsOneWidget);
    expect(find.text('Runtime'), findsNothing);
    expect(find.text('Budget'), findsNothing);
  });

  testWidgets(
      'a show reads created by, network and status, dates its hero, and '
      'lists its creators first in the sheet', (tester) async {
    await _pumpDetail(tester, type: MediaType.tv, body: _breakingBad);

    expect(find.textContaining('(2008)'), findsOneWidget);
    expect(find.text('Bryan Cranston'), findsOneWidget);
    expect(find.text('Walter White'), findsOneWidget);
    const lines = {
      'Status': 'Ended',
      'Created by': 'Vince Gilligan',
      'Network': 'AMC',
      'Country': 'United States of America',
    };
    for (final entry in lines.entries) {
      expect(find.text(entry.key), findsOneWidget, reason: entry.key);
      expect(find.text(entry.value), findsOneWidget, reason: entry.key);
    }
    expect(find.widgetWithText(ActionChip, 'Sony Pictures Television'),
        findsOneWidget);

    await _tap(tester, _seeAllFor('Cast'));
    expect(find.text('Creators'), findsOneWidget);
    // Details line, the Creators row, and his Production row: a creator who
    // also produces is listed under both departments.
    expect(find.text('Vince Gilligan'), findsNWidgets(3));
    expect(
      tester.getTopLeft(find.text('Creators')).dy,
      lessThan(tester.getTopLeft(find.text('Directing')).dy),
    );
  });
}

const _matrix = <String, dynamic>{
  'id': 603,
  'title': 'The Matrix',
  'release_date': '1999-03-30',
  'status': 'Released',
  'runtime': 136,
  'budget': 63000000,
  'revenue': 463517383,
  'genres': [
    {'id': 28, 'name': 'Action'},
  ],
  'production_companies': [
    {'id': 174, 'name': 'Warner Bros. Pictures'},
    {'id': 79, 'name': 'Village Roadshow Pictures'},
  ],
  'production_countries': [
    {'iso_3166_1': 'US', 'name': 'United States of America'},
  ],
  'credits': {
    'cast': [
      {
        'id': 6384,
        'name': 'Keanu Reeves',
        'character': 'Neo',
        'order': 0,
        'profile_path': null,
      },
      {
        'id': 2975,
        'name': 'Laurence Fishburne',
        'character': 'Morpheus',
        'order': 1,
        'profile_path': null,
      },
      {
        'id': 530,
        'name': 'Carrie-Anne Moss',
        'character': 'Trinity',
        'order': 2,
        'profile_path': null,
      },
    ],
    'crew': [
      {
        'id': 9339,
        'name': 'Lana Wachowski',
        'job': 'Director',
        'department': 'Directing',
        'profile_path': null,
      },
      {
        'id': 9340,
        'name': 'Lilly Wachowski',
        'job': 'Director',
        'department': 'Directing',
        'profile_path': null,
      },
      {
        'id': 9339,
        'name': 'Lana Wachowski',
        'job': 'Screenplay',
        'department': 'Writing',
        'profile_path': null,
      },
      {
        'id': 1091,
        'name': 'Joel Silver',
        'job': 'Producer',
        'department': 'Production',
        'profile_path': null,
      },
    ],
  },
};

const _breakingBad = <String, dynamic>{
  'id': 1396,
  'name': 'Breaking Bad',
  'first_air_date': '2008-01-20',
  'status': 'Ended',
  'genres': [
    {'id': 18, 'name': 'Drama'},
  ],
  'seasons': <dynamic>[],
  'created_by': [
    {'id': 66633, 'name': 'Vince Gilligan', 'profile_path': null},
  ],
  'networks': [
    {'id': 174, 'name': 'AMC'},
  ],
  'production_companies': [
    {'id': 11073, 'name': 'Sony Pictures Television'},
  ],
  'production_countries': [
    {'iso_3166_1': 'US', 'name': 'United States of America'},
  ],
  'credits': {
    'cast': [
      {
        'id': 17419,
        'name': 'Bryan Cranston',
        'character': 'Walter White',
        'order': 0,
        'profile_path': null,
      },
    ],
    'crew': [
      {
        'id': 66633,
        'name': 'Vince Gilligan',
        'job': 'Executive Producer',
        'department': 'Production',
        'profile_path': null,
      },
      {
        'id': 1223198,
        'name': 'Michelle MacLaren',
        'job': 'Director',
        'department': 'Directing',
        'profile_path': null,
      },
    ],
  },
};

Finder _seeAllFor(String rowTitle) => find.byWidgetPredicate(
      (w) => w is SeeAllButton && w.rowTitle == rowTitle,
    );

/// The list inside the open cast and crew sheet, as distinct from the
/// page's own scroll view and the Cast row underneath it.
Finder _sheetScrollable() => find.descendant(
      of: find.byType(DraggableScrollableSheet),
      matching: find.byType(Scrollable),
    );

/// Scrolls [finder] into view, lets the scroll finish, then taps it.
Future<void> _tap(WidgetTester tester, Finder finder) async {
  await tester.ensureVisible(finder);
  await tester.pumpAndSettle();
  await tester.tap(finder);
  await tester.pumpAndSettle();
}

Future<void> _pumpDetail(
  WidgetTester tester, {
  required MediaType type,
  required Map<String, dynamic> body,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final router = GoRouter(
    initialLocation: '/detail/${type.name}/${body['id']}',
    routes: [
      GoRoute(
        path: '/detail/:type/:id',
        builder: (_, state) => MediaDetailScreen(
          id: int.parse(state.pathParameters['id']!),
          mediaType: type,
        ),
      ),
    ],
  );

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = _DetailAdapter(body);

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(_state)),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      ],
      child: MaterialApp.router(routerConfig: router),
    ),
  );
  await tester.pumpAndSettle();
}

const _state = AuthState(
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
    ],
  ),
  user: UserProfile(id: 1, username: 'viewer', role: 'user'),
);

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

/// Serves one title's detail body, empty rows, and the bare person bodies
/// the person sheet needs to stop loading. The credits branch comes before
/// the person branch because both paths contain `/api/media/person/`.
class _DetailAdapter implements HttpClientAdapter {
  final Map<String, dynamic> body;

  _DetailAdapter(this.body);

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.path;
    final Object response;
    if (path.endsWith('/status')) {
      response = {'status': 'available', 'seasons': <dynamic>[]};
    } else if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      response = {'results': <dynamic>[]};
    } else if (path.endsWith('/credits')) {
      response = <String, dynamic>{};
    } else if (path.contains('/api/media/person/')) {
      response = {'id': int.parse(path.split('/').last)};
    } else if (path.contains('/api/media/movie/') ||
        path.contains('/api/media/tv/')) {
      response = body;
    } else {
      response = <dynamic>[];
    }
    return ResponseBody.fromString(
      jsonEncode(response),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
