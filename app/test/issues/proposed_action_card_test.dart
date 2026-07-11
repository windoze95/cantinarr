import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/issues/data/agent_action_models.dart';
import 'package:cantinarr/features/issues/data/issues_service.dart';
import 'package:cantinarr/features/issues/logic/issues_provider.dart';
import 'package:cantinarr/features/issues/ui/proposed_action_card.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Builds a proposed grab_release action for the card under test.
AgentAction _proposed() => AgentAction.fromJson({
      'id': 12,
      'issue_id': 5,
      'run_id': 9,
      'kind': 'grab_release',
      'params': {
        'media_type': 'tv',
        'guid': '[REDACTED release sha256:0123456789abcdef]',
        'indexer_id': 2,
        'queue_id_to_replace': 44,
        'release_title': 'Show.S02E04.1080p.WEB.H264-GROUP',
        'quality': 'WEBDL-1080p',
        'size': 2147483648,
        'protocol': 'usenet',
        'indexer': 'Example Indexer',
        'rejected': false,
      },
      'rationale':
          'The current release has Russian audio; this one is English.',
      'risk': 'mutating',
      'status': 'proposed',
      'can_decide': true,
      'issue_status': 'awaiting_approval',
      'created_at': '2026-06-23T10:00:00Z',
      'issue_title': 'The Show',
      'issue_media_type': 'tv',
      'issue_category': 'wrong_audio',
      'instance_id': 'sonarr-living-room',
      'instance_name': 'Living Room TV',
      'instance_service_type': 'sonarr',
    });

AgentAction _episodeSearch() => AgentAction.fromJson({
      'id': 13,
      'issue_id': 5,
      'kind': 'trigger_search',
      'params': {
        'media_type': 'tv',
        'tmdb_id': 42,
        'season': 2,
        'episode': 7,
      },
      'rationale': 'Search for a replacement of only the reported episode.',
      'status': 'proposed',
      'can_decide': true,
      'issue_status': 'awaiting_approval',
      'issue_title': 'The Show',
      'issue_media_type': 'tv',
      'instance_id': 'sonarr-living-room',
      'instance_name': 'Living Room TV',
      'instance_service_type': 'sonarr',
    });

/// A fake service that returns canned decision results without any network I/O.
class _FakeIssuesService extends IssuesService {
  _FakeIssuesService() : super(backendDio: Dio());

  AgentAction Function(AgentAction)? onDeny;
  AgentAction Function(AgentAction)? onApprove;
  AgentAction Function(AgentAction)? onGet;
  Object? approveError;
  Object? denyError;

  @override
  Future<AgentAction> denyAction(int id, {String? note}) async {
    final error = denyError;
    if (error != null) throw error;
    final base = _proposed();
    return (onDeny ?? _denied)(base);
  }

  @override
  Future<AgentAction> approveAction(int id, {Object? override}) async {
    final error = approveError;
    if (error != null) throw error;
    final base = _proposed();
    return (onApprove ?? _executed)(base);
  }

  @override
  Future<AgentAction> getAction(int id) async {
    final base = _proposed();
    return (onGet ?? _executed)(base);
  }

  static AgentAction _denied(AgentAction _) => AgentAction.fromJson({
        'id': 12,
        'issue_id': 5,
        'kind': 'grab_release',
        'params': const {},
        'status': 'denied',
        'deny_reason': 'Not the right release.',
        'decided_at': '2026-06-23T10:05:00Z',
        'instance_id': 'sonarr-living-room',
        'instance_name': 'Living Room TV',
        'instance_service_type': 'sonarr',
      });

  static AgentAction _executed(AgentAction _) => AgentAction.fromJson({
        'id': 12,
        'issue_id': 5,
        'kind': 'grab_release',
        'params': const {},
        'status': 'executed',
        'decided_at': '2026-06-23T10:05:00Z',
        'executed_at': '2026-06-23T10:05:02Z',
        'result_text': 'Grabbed the replacement release.',
        'instance_id': 'sonarr-living-room',
        'instance_name': 'Living Room TV',
        'instance_service_type': 'sonarr',
      });

  static AgentAction _failed(AgentAction _) => AgentAction.fromJson({
        'id': 12,
        'issue_id': 5,
        'kind': 'grab_release',
        'params': const {},
        'status': 'failed',
        'decided_at': '2026-06-23T10:05:00Z',
        'executed_at': '2026-06-23T10:05:02Z',
        'result_text': 'The connected service rejected the change.',
        'instance_id': 'sonarr-living-room',
        'instance_name': 'Living Room TV',
        'instance_service_type': 'sonarr',
      });
}

const _adminState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

const _userState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
  ),
  user: UserProfile(id: 2, username: 'reporter', role: 'user'),
);

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;
  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

/// Dismisses any in-flight SnackBar and lets its animation finish so a
/// transient toast can't leak its focus scope into the next test.
Future<void> _drainSnackBar(WidgetTester tester) async {
  final messengerState =
      tester.state<ScaffoldMessengerState>(find.byType(ScaffoldMessenger));
  messengerState.clearSnackBars();
  await tester.pumpAndSettle();
}

Future<void> _pump(
  WidgetTester tester, {
  required AuthState auth,
  required IssuesService service,
  required AgentAction action,
  bool decisionsEnabled = true,
}) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(() => _FakeAuthNotifier(auth)),
        issuesServiceProvider.overrideWithValue(service),
      ],
      child: MaterialApp(
        home: Scaffold(
          body: SingleChildScrollView(
            child: ProposedActionCard(
              action: action,
              decisionsEnabled: decisionsEnabled,
            ),
          ),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('renders kind, params, and rationale as passive data',
      (tester) async {
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: _proposed(),
    );

    // Plain-language, kind-driven summary (server copy, not an agent string).
    expect(find.text('Grab a different release and remove the current one'),
        findsOneWidget);
    // Server-observed candidate metadata is shown as quoted data; the raw
    // release capability never reaches the app.
    expect(find.textContaining('Show.S02E04.1080p'), findsOneWidget);
    expect(find.text('WEBDL-1080p'), findsOneWidget);
    expect(find.text('2.0 GB'), findsOneWidget);
    expect(find.text('Example Indexer (#2)'), findsOneWidget);
    // The agent's rationale is quoted verbatim.
    expect(find.textContaining('Russian audio'), findsOneWidget);
    // The immutable target is prominent and separate from agent text.
    expect(find.text('Target instance'), findsOneWidget);
    expect(find.text('Sonarr · Living Room TV'), findsOneWidget);
    expect(find.text('sonarr-living-room'), findsOneWidget);
    // Admin sees the two fixed controls.
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsOneWidget);
    expect(find.widgetWithText(OutlinedButton, 'Deny'), findsOneWidget);
  });

  testWidgets('renders episode scope for a trigger-search proposal',
      (tester) async {
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: _episodeSearch(),
    );

    expect(find.text('Season'), findsOneWidget);
    expect(find.text('2'), findsOneWidget);
    expect(find.text('Episode'), findsOneWidget);
    expect(find.text('7'), findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsOneWidget);
  });

  testWidgets('a non-admin sees a read-only "waiting on an admin" footer',
      (tester) async {
    await _pump(
      tester,
      auth: _userState,
      service: _FakeIssuesService(),
      action: _proposed(),
    );

    expect(find.text('Waiting on an admin to approve a fix.'), findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(find.widgetWithText(OutlinedButton, 'Deny'), findsNothing);
  });

  testWidgets('freezes after a deny decision and never re-enables',
      (tester) async {
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: _proposed(),
    );

    // Open the deny dialog and confirm.
    await tester.tap(find.widgetWithText(OutlinedButton, 'Deny'));
    await tester.pumpAndSettle();
    // The dialog's Deny button (distinct from the card's) confirms.
    await tester.tap(find.widgetWithText(ElevatedButton, 'Deny'));
    await tester.pump(); // run the decision future
    await tester.pump(); // rebuild frozen

    // The card is now frozen: a "Denied" footer, and the controls are gone.
    expect(find.textContaining('Denied'), findsWidgets);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(find.widgetWithText(OutlinedButton, 'Deny'), findsNothing);

    await _drainSnackBar(tester);
  });

  testWidgets('freezes after an approve decision (Approved · applied)',
      (tester) async {
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: _proposed(),
    );

    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    expect(find.text('Approve this change?'), findsOneWidget);
    expect(
      find.textContaining('download a different release'),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: find.byType(AlertDialog),
        matching: find.text('Sonarr · Living Room TV'),
      ),
      findsOneWidget,
    );
    expect(
      find.descendant(
        of: find.byType(AlertDialog),
        matching: find.text('sonarr-living-room'),
      ),
      findsOneWidget,
    );
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve and apply'));
    await tester.pump(); // run the decision future
    await tester.pump(); // rebuild frozen

    // The frozen footer (distinct from the transient "Approved — applying…"
    // SnackBar). The controls are gone.
    expect(find.textContaining('· applied'), findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(find.widgetWithText(OutlinedButton, 'Deny'), findsNothing);

    await _drainSnackBar(tester);
  });

  testWidgets('lost approval response is reconciled before allowing a retry',
      (tester) async {
    final service = _FakeIssuesService()
      ..approveError = DioException(
        requestOptions: RequestOptions(path: '/approve'),
        type: DioExceptionType.connectionError,
      )
      ..onGet = _FakeIssuesService._executed;
    await _pump(
      tester,
      auth: _adminState,
      service: service,
      action: _proposed(),
    );

    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve and apply'));
    await tester.pump();
    await tester.pump();

    expect(find.textContaining('· applied'), findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(find.text('Fix applied.'), findsOneWidget);
    await _drainSnackBar(tester);
  });

  testWidgets('approval toast reports a failed execution', (tester) async {
    final service = _FakeIssuesService()
      ..onApprove = _FakeIssuesService._failed;
    await _pump(
      tester,
      auth: _adminState,
      service: service,
      action: _proposed(),
    );

    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve and apply'));
    await tester.pump();
    await tester.pump();

    expect(find.text('The fix was approved, but it failed.'), findsOneWidget);
    expect(find.text('Approved, but the fix failed'), findsOneWidget);
    await _drainSnackBar(tester);
  });

  testWidgets(
      'deny conflict reconciles an approval winner without claiming denied',
      (tester) async {
    final service = _FakeIssuesService()
      ..denyError = DioException.badResponse(
        statusCode: 409,
        requestOptions: RequestOptions(path: '/deny'),
        response: Response(
          requestOptions: RequestOptions(path: '/deny'),
          statusCode: 409,
          data: {'error': 'action decision conflict: action is now executed'},
        ),
      )
      ..onGet = _FakeIssuesService._executed;
    await _pump(
      tester,
      auth: _adminState,
      service: service,
      action: _proposed(),
    );

    await tester.tap(find.widgetWithText(OutlinedButton, 'Deny'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Deny'));
    await tester.pump();
    await tester.pump();

    expect(find.text('Fix applied.'), findsOneWidget);
    expect(
      find.text('Fix denied. The agent can investigate another option.'),
      findsNothing,
    );
    expect(find.textContaining('· applied'), findsOneWidget);
    await _drainSnackBar(tester);
  });

  testWidgets('unknown or malformed proposals explain why they are read-only',
      (tester) async {
    final malformed = AgentAction.fromJson({
      'id': 14,
      'issue_id': 5,
      'kind': 'grab_release',
      'params': '{bad-json',
      'status': 'proposed',
      'can_decide': true,
      'issue_status': 'awaiting_approval',
    });
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: malformed,
    );

    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(find.text('The proposed fix data is malformed.'), findsOneWidget);
  });

  testWidgets('a retained stale proposal is read-only until refreshed',
      (tester) async {
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: _proposed(),
      decisionsEnabled: false,
    );

    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(find.widgetWithText(OutlinedButton, 'Deny'), findsNothing);
    expect(
      find.text('This fix could not be refreshed. Retry before deciding.'),
      findsOneWidget,
    );
  });

  testWidgets('an already-decided action renders frozen for an admin',
      (tester) async {
    final decided = AgentAction.fromJson({
      'id': 13,
      'issue_id': 5,
      'kind': 'rescan',
      'params': {'media_type': 'movie', 'tmdb_id': 27205},
      'status': 'executed',
      'decided_at': '2026-06-23T10:05:00Z',
      'executed_at': '2026-06-23T10:05:02Z',
      'result_text': 'Rescan triggered.',
    });
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: decided,
    );

    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(find.textContaining('Approved'), findsOneWidget);
    expect(find.textContaining('Rescan triggered.'), findsOneWidget);
  });
}
