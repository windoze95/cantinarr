import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/issues/data/issue_models.dart';
import 'package:cantinarr/features/issues/data/issues_service.dart';
import 'package:cantinarr/features/issues/logic/issues_provider.dart';
import 'package:cantinarr/features/issues/ui/issue_thread_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// The reporter — deliberately not an admin. The confirm control belongs to the
/// person whose judgment the issue records, not to whoever reviews it.
const _reporterState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
  ),
  user: UserProfile(id: 3, username: 'alice', role: 'user'),
);

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => _reporterState;
}

/// The agent's closing message — the question the confirm control answers.
const _agentAsk =
    'I applied the approved fix. Whether it’s right now is your call '
    'rather than something I can prove — have a look, and tap "This is fixed" '
    'if the content is what you expected. If it still isn’t, reply and '
    'tell me what you see.';

Map<String, dynamic> _openIssue({required bool canConfirmFixed}) => {
      'id': 5,
      'source': 'user',
      'status': 'needs_admin',
      'category': 'wrong_content',
      'reporter_id': 3,
      'reporter_name': 'alice',
      'instance_id': 'sonarr-main',
      'tmdb_id': 123,
      'media_type': 'tv',
      'title': 'Example Show',
      'season_number': 2,
      'episode_number': 4,
      'detail': 'This is the wrong episode.',
      'occurrences': 1,
      'read': false,
      'resolution': '',
      'resolution_kind': '',
      'can_confirm_fixed': canConfirmFixed,
      'created_at': '2026-07-10T10:00:00Z',
      'updated_at': '2026-07-10T10:05:00Z',
      'closed_at': null,
    };

IssueThread _openThread({required bool canConfirmFixed}) =>
    IssueThread.fromJson({
      'issue': _openIssue(canConfirmFixed: canConfirmFixed),
      'thread': [
        {
          'id': 1,
          'author_kind': 'agent',
          'author_name': 'Cantinarr',
          'body': _agentAsk,
          'created_at': '2026-07-10T10:05:00Z',
        },
      ],
    });

/// What the server returns once the reporter's verdict is recorded: closed,
/// with the confirmation kept in the thread as their own message.
IssueThread _confirmedThread() => IssueThread.fromJson({
      'issue': {
        ..._openIssue(canConfirmFixed: false),
        'status': 'resolved',
        'resolution': 'The reporter confirmed the fix worked.',
        'resolution_kind': 'reporter_confirmed',
        'closed_at': '2026-07-10T11:00:00Z',
      },
      'thread': [
        {
          'id': 1,
          'author_kind': 'agent',
          'author_name': 'Cantinarr',
          'body': _agentAsk,
          'created_at': '2026-07-10T10:05:00Z',
        },
        {
          'id': 2,
          'author_kind': 'user',
          'author_name': 'alice',
          'body': 'I checked, and this is fixed.',
          'created_at': '2026-07-10T11:00:00Z',
        },
      ],
    });

/// The thread as another closer left it: already terminal when the reporter's
/// confirmation lands.
IssueThread _closedByAdminThread() => IssueThread.fromJson({
      'issue': {
        ..._openIssue(canConfirmFixed: false),
        'status': 'resolved',
        'resolution': 'Verified playback manually.',
        'resolution_kind': 'admin_completed',
        'closed_at': '2026-07-10T10:59:00Z',
      },
      'thread': const [],
    });

class _FakeIssuesService extends IssuesService {
  _FakeIssuesService({required this.thread}) : super(backendDio: Dio());

  IssueThread thread;

  /// What a later read returns — the confirmed close, or whichever state won.
  IssueThread? threadAfterConfirm;
  Object? confirmError;
  int confirmCalls = 0;
  int threadLoads = 0;

  @override
  Future<IssueThread> getThread(int id) async {
    threadLoads++;
    return thread;
  }

  @override
  Future<void> confirmFixed(int issueId) async {
    confirmCalls++;
    final next = threadAfterConfirm;
    final error = confirmError;
    if (error != null) {
      if (next != null) thread = next;
      throw error;
    }
    if (next != null) thread = next;
  }
}

DioException _conflict(String message) {
  final options = RequestOptions(path: '/api/issues/5/confirm-fixed');
  return DioException.badResponse(
    statusCode: 409,
    requestOptions: options,
    response: Response(
      requestOptions: options,
      statusCode: 409,
      data: {'error': message},
    ),
  );
}

Future<void> _pumpThread(
  WidgetTester tester,
  _FakeIssuesService service,
) async {
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(_FakeAuthNotifier.new),
      issuesServiceProvider.overrideWithValue(service),
      realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
    ],
  );
  addTearDown(container.dispose);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: IssueThreadScreen(issueId: 5)),
    ),
  );
  await tester.pumpAndSettle();
}

Finder get _confirmButton =>
    find.widgetWithText(ElevatedButton, 'This is fixed');

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('no confirm control until the server says a fix was applied',
      (tester) async {
    final service = _FakeIssuesService(
      thread: _openThread(canConfirmFixed: false),
    );

    await _pumpThread(tester, service);

    expect(_confirmButton, findsNothing);
    expect(find.text('Is it right now?'), findsNothing);
    // The reply path is the only answer available until then.
    expect(find.text('Add a reply…'), findsOneWidget);
  });

  testWidgets('the confirm control asks, and never replaces the reply path',
      (tester) async {
    final service = _FakeIssuesService(
      thread: _openThread(canConfirmFixed: true),
    );

    await _pumpThread(tester, service);

    expect(_confirmButton, findsOneWidget);
    expect(find.text('Is it right now?'), findsOneWidget);
    expect(find.textContaining('Still not right? Reply below'), findsOneWidget);
    // "No, still wrong" stays a reply, unchanged and obvious.
    expect(find.text('Add a reply…'), findsOneWidget);
    expect(find.byIcon(Icons.send_rounded), findsOneWidget);
    // Never dressed up as the admin's completion judgment.
    expect(find.text('Complete this issue'), findsNothing);
    expect(find.text('Complete after admin review'), findsNothing);
    expect(find.widgetWithText(OutlinedButton, 'Close without fix'),
        findsNothing);
  });

  testWidgets('confirming warns it is final and can be backed out of',
      (tester) async {
    final service = _FakeIssuesService(
      thread: _openThread(canConfirmFixed: true),
    )..threadAfterConfirm = _confirmedThread();

    await _pumpThread(tester, service);
    await tester.tap(_confirmButton);
    await tester.pumpAndSettle();

    final dialog = find.byType(AlertDialog);
    expect(
      find.descendant(of: dialog, matching: find.text('Close this as fixed?')),
      findsOneWidget,
    );
    // There is no reopen anywhere in this product, so the dialog must say the
    // thread ends and point at the only way back in.
    expect(find.textContaining('can’t be reopened'), findsOneWidget);
    expect(find.textContaining('won’t be able to reply'), findsOneWidget);
    expect(find.textContaining('report the problem again'), findsOneWidget);

    await tester.tap(find.widgetWithText(TextButton, 'Not yet'));
    await tester.pumpAndSettle();

    expect(service.confirmCalls, 0);
    expect(_confirmButton, findsOneWidget);
    expect(find.text('Add a reply…'), findsOneWidget);
  });

  testWidgets('a confirmed fix closes the thread and shows the new state',
      (tester) async {
    final service = _FakeIssuesService(
      thread: _openThread(canConfirmFixed: true),
    )..threadAfterConfirm = _confirmedThread();

    await _pumpThread(tester, service);
    final loadsBefore = service.threadLoads;
    await tester.tap(_confirmButton);
    await tester.pumpAndSettle();
    await tester.tap(find.descendant(
      of: find.byType(AlertDialog),
      matching: find.widgetWithText(ElevatedButton, 'Yes, it’s fixed'),
    ));
    await tester.pumpAndSettle();

    expect(service.confirmCalls, 1);
    // Refreshed, so the closed state and the recorded confirmation render.
    expect(service.threadLoads, greaterThan(loadsBefore));
    expect(find.text('I checked, and this is fixed.'), findsOneWidget);
    expect(find.textContaining('Confirmed fixed'), findsOneWidget);
    expect(find.text('This issue is closed.'), findsOneWidget);
    expect(find.text('Add a reply…'), findsNothing);
    expect(_confirmButton, findsNothing);
    expect(find.text('Thanks for checking. This is closed.'), findsOneWidget);
  });

  testWidgets('a refused confirmation reports the server reason and refreshes',
      (tester) async {
    const reason = 'this issue was already closed';
    final service = _FakeIssuesService(
      thread: _openThread(canConfirmFixed: true),
    )
      ..threadAfterConfirm = _closedByAdminThread()
      ..confirmError = _conflict(reason);

    await _pumpThread(tester, service);
    await tester.tap(_confirmButton);
    await tester.pumpAndSettle();
    await tester.tap(find.descendant(
      of: find.byType(AlertDialog),
      matching: find.widgetWithText(ElevatedButton, 'Yes, it’s fixed'),
    ));
    await tester.pumpAndSettle();

    expect(service.confirmCalls, 1);
    expect(find.text(reason), findsOneWidget);
    // Never claims the reporter's verdict was the one recorded...
    expect(find.text('Thanks for checking. This is closed.'), findsNothing);
    expect(find.textContaining('Completed after review'), findsOneWidget);
    // ...and the control the server just refused is gone.
    expect(_confirmButton, findsNothing);
  });
}
