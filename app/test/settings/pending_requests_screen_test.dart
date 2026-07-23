import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/storage/preferences.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:cantinarr/features/settings/data/request_settings_service.dart';
import 'package:cantinarr/features/settings/ui/pending_requests_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('blank requester names use safe, trimmed approval copy', () {
    PendingRequestItem item(String username, int requesterCount) =>
        PendingRequestItem.fromJson({
          'username': username,
          'requester_count': requesterCount,
        });

    expect(item('  reader  ', 1).requestedByLabel, 'Requested by reader');
    expect(item('   ', 1).requestedByLabel, 'Requested by a user');
    expect(
      item('', 2).requestedByLabel,
      'Requested by a user and 1 other',
    );
  });

  testWidgets(
      'empty approvals list keeps its menu visibility control available',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _ApprovalsAdapter();
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('No pending requests.'), findsOneWidget);
    final toggle = find.byKey(
      const ValueKey('approvals-conditional-menu-visibility'),
    );
    expect(toggle, findsOneWidget);
    expect(tester.widget<SwitchListTile>(toggle).value, isFalse);

    await tester.tap(toggle);
    await tester.pumpAndSettle();

    expect(container.read(approvalsMenuOnlyWhenPendingProvider), isTrue);
    expect(tester.widget<SwitchListTile>(toggle).value, isTrue);
  });

  testWidgets('an unknown book format is visible and cannot be approved',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    dio.httpClientAdapter = _ApprovalsAdapter(pending: [
      {
        'id': 7,
        'user_id': 2,
        'username': 'reader',
        'media_type': 'book',
        'title': 'Flock',
        'book_format': 'future-format',
      },
    ]);
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Unsupported format'), findsOneWidget);
    final approve = tester.widget<IconButton>(
      find.ancestor(
        of: find.byIcon(Icons.check_circle_outline),
        matching: find.byType(IconButton),
      ),
    );
    expect(approve.onPressed, isNull);
  });

  testWidgets(
      'book approval preserves the requested format and names its library',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final adapter = _ApprovalsAdapter(
      pending: [
        {
          'id': 7,
          'user_id': 2,
          'username': 'reader',
          'media_type': 'book',
          'title': 'Flock',
          'book_format': 'both',
          'instance_name': 'Family Books',
          'requester_count': 3,
          'book_selection': {
            'foreign_author_id': 'author-external-17',
            'author_name': 'Tarryn Fisher',
            'ebook': {
              'foreign_edition_id': 'edition-ebook-2',
              'edition_title': 'Anniversary Edition',
              'publisher': 'Graydon House',
              'isbn13': '9781525809781',
            },
            'audiobook': {
              'foreign_edition_id': 'edition-audio-3',
              'publisher': 'Harlequin Audio',
              'asin': 'B08WJQ3M2L',
            },
          },
        },
      ],
      approvalResponse: const {
        'status': 'partial',
        'book_formats': {
          'ebook': 'pending',
          'audiobook': 'requested',
        },
      },
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);

    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('Library: Family Books'), findsOneWidget);
    expect(find.text('Requested by reader and 2 others'), findsOneWidget);
    expect(find.text('Author: Tarryn Fisher'), findsOneWidget);
    expect(
      find.text(
        'eBook: Anniversary Edition · Graydon House · ISBN 9781525809781',
      ),
      findsOneWidget,
    );
    expect(
      find.text('Audiobook: Harlequin Audio · ASIN B08WJQ3M2L'),
      findsOneWidget,
    );
    expect(find.textContaining('author-external-17'), findsNothing);
    expect(find.textContaining('edition-ebook-2'), findsNothing);
    await tester.tap(find.byIcon(Icons.check_circle_outline));
    await tester.pumpAndSettle();

    final dialog = find.byType(AlertDialog);
    expect(dialog, findsOneWidget);
    expect(find.descendant(of: dialog, matching: find.text('Requested format')),
        findsOneWidget);
    expect(find.descendant(
            of: dialog, matching: find.text('eBook + Audiobook')),
        findsOneWidget);
    expect(
      find.descendant(
        of: dialog,
        matching: find.text('Library: Family Books'),
      ),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: dialog,
        matching: find.text('Requested by reader and 2 others'),
      ),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: dialog,
        matching: find.text('Selected publication'),
      ),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: dialog,
        matching: find.text('Author: Tarryn Fisher'),
      ),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: dialog,
        matching: find.text(
          'eBook: Anniversary Edition · Graydon House · ISBN 9781525809781',
        ),
      ),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: dialog,
        matching: find.text(
          'Audiobook: Harlequin Audio · ASIN B08WJQ3M2L',
        ),
      ),
      findsOneWidget,
    );
    expect(find.byType(DropdownButtonFormField<BookRequestFormat>),
        findsNothing);

    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();

    expect(adapter.approvalBodies, hasLength(1));
    expect(adapter.approvalBodies.single, isEmpty);
    expect(
      find.text('Audiobook approved. eBook still needs attention.'),
      findsOneWidget,
    );
  });

  testWidgets('book approval errors use safe guidance from response JSON',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final adapter = _ApprovalsAdapter(
      pending: const [
        {
          'id': 7,
          'user_id': 2,
          'username': 'reader',
          'media_type': 'book',
          'title': 'Flock',
          'book_format': 'ebook',
        },
      ],
      approvalStatusCode: 409,
      approvalResponse: const {
        'error': 'pending book request has no pinned Chaptarr instance',
      },
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    Future<void> approve() async {
      await tester.tap(find.byIcon(Icons.check_circle_outline));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
      await tester.pumpAndSettle();
    }

    await approve();
    expect(
      find.text(
        'This older request doesn’t identify a book library; deny it and ask the requester to submit it again.',
      ),
      findsOneWidget,
    );
    tester
        .state<ScaffoldMessengerState>(find.byType(ScaffoldMessenger))
        .removeCurrentSnackBar();
    await tester.pumpAndSettle();

    adapter.approvalResponse = {
      'error': 'quality profile selection is ambiguous',
    };
    await approve();
    expect(
      find.text('Check this book library’s paths and profiles, then try again.'),
      findsOneWidget,
    );

    final codedFailures = <(String, String)>[
      (
        'book_selection_invalid',
        'This version choice is no longer valid. Deny this request and ask the requester to search for the book again.',
      ),
      (
        'book_edition_unavailable',
        'The selected edition is no longer available. Deny this request and ask the requester to choose another version.',
      ),
      (
        'book_format_unresolved',
        'The selected version is not identified as an eBook or audiobook. Deny this request and ask the requester to choose another version.',
      ),
      (
        'book_multi_work_unsupported',
        'This result contains multiple books. Deny this request and ask the requester to choose one individual title.',
      ),
      (
        'book_request_unverified',
        'Cantinarr could not verify the selected edition, so no download search started. Try approval again; if it keeps failing, check the book library.',
      ),
    ];
    for (final (code, message) in codedFailures) {
      tester
          .state<ScaffoldMessengerState>(find.byType(ScaffoldMessenger))
          .removeCurrentSnackBar();
      await tester.pumpAndSettle();
      adapter.approvalResponse = {
        'code': code,
        'error': 'server wording intentionally ignored',
      };

      await approve();

      expect(find.text(message), findsOneWidget, reason: code);
      if (code == 'book_request_unverified') {
        expect(find.textContaining('still confirming'), findsNothing);
      }
    }
  });

  testWidgets('an interrupted book approval reconciles the queue without retrying',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });
    final adapter = _ApprovalsAdapter(
      pending: const [
        {
          'id': 7,
          'user_id': 2,
          'username': 'reader',
          'media_type': 'book',
          'title': 'Flock',
          'book_format': 'audiobook',
        },
      ],
      approvalStatusCode: 500,
      approvalResponse: const {'error': 'response was interrupted'},
      removePendingBeforeApprovalError: true,
    );
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(
          const Stream<WsEvent>.empty(),
        ),
      ],
    );
    addTearDown(container.dispose);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const MaterialApp(home: PendingRequestsScreen()),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.check_circle_outline));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();

    expect(adapter.approvalBodies, hasLength(1));
    expect(find.text('No pending requests.'), findsOneWidget);
    expect(
      find.text('Approval completed. The remaining queue was refreshed.'),
      findsOneWidget,
    );
  });
}

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
        ),
        user: UserProfile(id: 1, username: 'admin', role: 'admin'),
      );
}

class _ApprovalsAdapter implements HttpClientAdapter {
  final List<Map<String, dynamic>> pending;
  Map<String, dynamic> approvalResponse;
  final int approvalStatusCode;
  final bool removePendingBeforeApprovalError;
  final List<Map<String, dynamic>> approvalBodies = [];

  _ApprovalsAdapter({
    List<Map<String, dynamic>> pending = const [],
    this.approvalResponse = const {},
    this.approvalStatusCode = 200,
    this.removePendingBeforeApprovalError = false,
  }) : pending = List.of(pending);

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'POST' &&
        options.uri.path == '/api/admin/requests/7/approve') {
      final bytes = <int>[];
      if (requestStream != null) {
        await for (final chunk in requestStream) {
          bytes.addAll(chunk);
        }
      }
      approvalBodies.add(
        bytes.isEmpty
            ? <String, dynamic>{}
            : jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>,
      );
      if (removePendingBeforeApprovalError) pending.clear();
    }
    final body = switch (options.uri.path) {
      '/api/admin/requests' => pending,
      '/api/admin/requests/7/approve' => approvalResponse,
      '/api/admin/request-settings' => {
          'settings': const <String, dynamic>{},
          'radarr_profiles': const <dynamic>[],
          'sonarr_profiles': const <dynamic>[],
        },
      _ => const <String, dynamic>{},
    };
    final statusCode = options.uri.path == '/api/admin/requests/7/approve'
        ? approvalStatusCode
        : 200;
    return ResponseBody.fromString(
      jsonEncode(body),
      statusCode,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
