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
        'guid': 'Show.S02E04.1080p.WEB.H264-GROUP',
        'indexer_id': 2,
        'queue_id_to_replace': 44,
      },
      'rationale': 'The current release has Russian audio; this one is English.',
      'risk': 'mutating',
      'status': 'proposed',
      'created_at': '2026-06-23T10:00:00Z',
      'issue_title': 'The Show',
      'issue_media_type': 'tv',
      'issue_category': 'wrong_audio',
    });

/// A fake service that returns canned decision results without any network I/O.
class _FakeIssuesService extends IssuesService {
  _FakeIssuesService() : super(backendDio: Dio());

  AgentAction Function(AgentAction)? onDeny;
  AgentAction Function(AgentAction)? onApprove;

  @override
  Future<AgentAction> denyAction(int id, {String? note}) async {
    final base = _proposed();
    return (onDeny ?? _denied)(base);
  }

  @override
  Future<AgentAction> approveAction(int id, {Object? override}) async {
    final base = _proposed();
    return (onApprove ?? _executed)(base);
  }

  static AgentAction _denied(AgentAction _) => AgentAction.fromJson({
        'id': 12,
        'issue_id': 5,
        'kind': 'grab_release',
        'params': const {},
        'status': 'denied',
        'deny_reason': 'Not the right release.',
        'decided_at': '2026-06-23T10:05:00Z',
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
            child: ProposedActionCard(action: action),
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
    // The release GUID is shown as quoted data.
    expect(find.textContaining('Show.S02E04.1080p'), findsOneWidget);
    // The agent's rationale is quoted verbatim.
    expect(find.textContaining('Russian audio'), findsOneWidget);
    // Admin sees the two fixed controls.
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsOneWidget);
    expect(find.widgetWithText(OutlinedButton, 'Deny'), findsOneWidget);
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
    await tester.pump(); // run the decision future
    await tester.pump(); // rebuild frozen

    // The frozen footer (distinct from the transient "Approved — applying…"
    // SnackBar). The controls are gone.
    expect(find.textContaining('· applied'), findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(find.widgetWithText(OutlinedButton, 'Deny'), findsNothing);

    await _drainSnackBar(tester);
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
