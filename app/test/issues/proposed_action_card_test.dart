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

AgentAction _specialSearch() => AgentAction.fromJson({
      'id': 14,
      'issue_id': 5,
      'kind': 'trigger_search',
      'params': {
        'media_type': 'tv',
        'tmdb_id': 42,
        'season': 0,
        'episode': 1,
      },
      'rationale': 'Search only for the reported special.',
      'status': 'proposed',
      'can_decide': true,
      'issue_status': 'awaiting_approval',
      'issue_title': 'The Show',
      'issue_media_type': 'tv',
      'instance_id': 'sonarr-living-room',
      'instance_name': 'Living Room TV',
      'instance_service_type': 'sonarr',
    });

/// A trigger_search proposal that still carries the removed `aired_only` flag.
/// The server can no longer emit it, so the card must treat it as an
/// unrecognized field and refuse to offer a decision.
AgentAction _staleAiredOnlySearch() => AgentAction.fromJson({
      'id': 15,
      'issue_id': 5,
      'kind': 'trigger_search',
      'params': {
        'media_type': 'tv',
        'tmdb_id': 615,
        'season': 11,
        'aired_only': true,
      },
      'rationale': 'Search only what has come out so far.',
      'status': 'proposed',
      'can_decide': true,
      'issue_status': 'awaiting_approval',
      'issue_title': 'The Show',
      'issue_media_type': 'tv',
      'instance_id': 'sonarr-living-room',
      'instance_name': 'Living Room TV',
      'instance_service_type': 'sonarr',
    });

/// A deletion of files the media service already imported. [blocklist] is the
/// facet under test: the same nine files either just go, or go and take their
/// releases out of circulation with them.
AgentAction _deleteFiles({required bool blocklist}) => AgentAction.fromJson({
      'id': 16,
      'issue_id': 5,
      'kind': 'delete_media_files',
      'params': {
        'media_type': 'tv',
        'tmdb_id': 615,
        'season': 11,
        'episodes': [1, 2, 3, 4, 5, 6, 7, 8, 9],
        'blocklist': blocklist,
      },
      'rationale': 'Every file was imported before its episode aired.',
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

  /// The `remember` value of the last approveAction call, so tests can prove
  /// the dialog checkbox (and only the checkbox) arms a standing rule.
  bool? lastRemember;

  /// The `override` of the last approveAction call — the episode-sparing edit.
  Object? lastOverride;

  @override
  Future<AgentAction> denyAction(int id, {String? note}) async {
    final error = denyError;
    if (error != null) throw error;
    final base = _proposed();
    return (onDeny ?? _denied)(base);
  }

  @override
  Future<AgentAction> approveAction(
    int id, {
    Object? override,
    bool remember = false,
  }) async {
    lastRemember = remember;
    lastOverride = override;
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

  testWidgets('renders an exact S00 special as an approvable proposal',
      (tester) async {
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: _specialSearch(),
    );

    expect(find.text('Season'), findsOneWidget);
    expect(find.text('0'), findsOneWidget);
    expect(find.text('Episode'), findsOneWidget);
    expect(find.text('1'), findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsOneWidget);
  });

  testWidgets('a search still carrying aired_only is frozen, not decidable',
      (tester) async {
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: _staleAiredOnlySearch(),
    );

    // No scope row survives, and the extra key blocks the decision outright
    // instead of being silently dropped from a fix an admin then authorises.
    expect(find.text('Scope'), findsNothing);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
    expect(
      find.textContaining('does not recognize'),
      findsOneWidget,
    );
  });

  testWidgets('a blocklisting deletion states the exact, irreversible target',
      (tester) async {
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: _deleteFiles(blocklist: true),
    );

    expect(
      find.text('Delete the wrong files, block those releases from coming '
          'back, and look for replacements'),
      findsOneWidget,
    );
    // The exact target, from validated integers — the show's title is not in
    // the params, so season + episode numbers + count carry it.
    expect(find.text('Season'), findsOneWidget);
    expect(find.text('11'), findsOneWidget);
    expect(find.text('Episodes'), findsOneWidget);
    expect(find.text('1–9'), findsOneWidget);
    expect(find.text('Files to delete'), findsOneWidget);
    expect(find.text('9'), findsOneWidget);
    expect(find.text('Block the release'), findsOneWidget);
    expect(find.text('yes'), findsOneWidget);
    // An irreversible fix is still an approvable one.
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsOneWidget);

    await tester.ensureVisible(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    expect(
      find.textContaining(
          'permanently delete 9 files from your library — season 11, '
          'episodes 1–9'),
      findsOneWidget,
    );
    expect(
      find.textContaining(
          'This cannot be undone: the files are removed from disk'),
      findsOneWidget,
    );
    expect(
      find.textContaining(
          'will not download those same releases again'),
      findsOneWidget,
    );
    // The repair does not stop at the deletion: the same approval covers
    // getting back what has already aired, and only what has already aired.
    expect(
      find.textContaining('Cantinarr then looks for replacements, but only for '
          'the episodes of this season that have already aired. The rest of '
          'the season is left alone — your media service will grab each '
          'episode as it comes out.'),
      findsOneWidget,
    );
    // Blocking is the one thing that can make the media service search first,
    // and then Cantinarr stands down rather than duplicating it.
    expect(
      find.textContaining('If blocking those releases already sent your media '
          'service looking for replacements itself, Cantinarr leaves that to '
          'it.'),
      findsOneWidget,
    );
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
  });

  testWidgets('a files-only deletion warns the same release can return',
      (tester) async {
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: _deleteFiles(blocklist: false),
    );

    expect(
      find.text('Delete the wrong files already in your library and look for '
          'replacements'),
      findsOneWidget,
    );
    expect(find.text('Block the release'), findsOneWidget);
    expect(find.text('no'), findsOneWidget);
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsOneWidget);

    await tester.ensureVisible(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    expect(
      find.textContaining(
          'permanently delete 9 files from your library — season 11, '
          'episodes 1–9'),
      findsOneWidget,
    );
    expect(
      find.textContaining('This cannot be undone'),
      findsOneWidget,
    );
    expect(
      find.textContaining('The releases those files came from are not blocked'),
      findsOneWidget,
    );
    // The replacement half of the repair is promised either way.
    expect(
      find.textContaining('Cantinarr then looks for replacements, but only for '
          'the episodes of this season that have already aired. The rest of '
          'the season is left alone — your media service will grab each '
          'episode as it comes out.'),
      findsOneWidget,
    );
    // Nothing was blocked, so there is no media-service search to stand down
    // for — the card must not hedge a search it will definitely run.
    expect(
      find.textContaining('Cantinarr leaves that to it'),
      findsNothing,
    );
    await tester.tap(find.widgetWithText(TextButton, 'Cancel'));
    await tester.pumpAndSettle();
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

    await tester.ensureVisible(find.widgetWithText(ElevatedButton, 'Approve'));
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

    await tester.ensureVisible(find.widgetWithText(ElevatedButton, 'Approve'));
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

    await tester.ensureVisible(find.widgetWithText(ElevatedButton, 'Approve'));
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

  testWidgets('approve dialog has no remember checkbox without a server offer',
      (tester) async {
    final service = _FakeIssuesService();
    await _pump(
      tester,
      auth: _adminState,
      service: service,
      action: _proposed(),
    );

    await tester.ensureVisible(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    expect(find.byType(CheckboxListTile), findsNothing);
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve and apply'));
    await tester.pump();
    await tester.pump();

    // The wire contract for the plain path: remember is false.
    expect(service.lastRemember, isFalse);
    await _drainSnackBar(tester);
  });

  testWidgets('checked remember checkbox reaches the service', (tester) async {
    final service = _FakeIssuesService();
    await _pump(
      tester,
      auth: _adminState,
      service: service,
      action: _proposedWithOffer(),
    );

    await tester.ensureVisible(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    // The checkbox and its server-typed label render only because the server
    // sent an offer; its copy names the exact (fix, problem) pair.
    expect(find.byType(CheckboxListTile), findsOneWidget);
    expect(
      find.text('Always approve this fix for this problem'),
      findsOneWidget,
    );
    expect(
      find.textContaining('Manual import · Waiting to import'),
      findsOneWidget,
    );
    await tester.tap(find.byType(CheckboxListTile));
    await tester.pump();
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve and apply'));
    await tester.pump();
    await tester.pump();

    expect(service.lastRemember, isTrue);
    await _drainSnackBar(tester);
  });

  testWidgets('unchecked remember stays unchecked and reactivation is named',
      (tester) async {
    final service = _FakeIssuesService();
    await _pump(
      tester,
      auth: _adminState,
      service: service,
      action: _proposedWithOffer(reactivates: true),
    );

    await tester.ensureVisible(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    // A paused rule's offer says checking the box re-arms it.
    expect(find.textContaining('re-enables a paused rule'), findsOneWidget);
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve and apply'));
    await tester.pump();
    await tester.pump();

    expect(service.lastRemember, isFalse);
    await _drainSnackBar(tester);
  });

  testWidgets('rule-approved history reads Approved automatically',
      (tester) async {
    final autoDecided = AgentAction.fromJson({
      'id': 21,
      'issue_id': 5,
      'kind': 'manual_import',
      'params': {'media_type': 'movie', 'queue_id': 7},
      'status': 'executed',
      'decided_at': '2026-06-23T10:05:00Z',
      'executed_at': '2026-06-23T10:05:02Z',
      'result_text': 'Imported the downloaded files.',
      'auto_rule_id': 3,
      'auto_approved': true,
      'auto_rule_label': 'Manual import · Waiting to import',
      'instance_id': 'radarr-main',
      'instance_name': 'Main Movies',
      'instance_service_type': 'radarr',
    });
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: autoDecided,
    );

    // decided_by is null on a rule decision; the footer must attribute the
    // rule, never imply a human approved it.
    expect(
      find.textContaining(
          'Approved automatically · Manual import · Waiting to import'),
      findsOneWidget,
    );
    expect(find.widgetWithText(ElevatedButton, 'Approve'), findsNothing);
  });

  // The one edit with no wrong answer: unchecking an episode on a destructive
  // delete sends an approval override that spares it — never an empty delete.
  testWidgets('delete card episode chips build a sparing override',
      (tester) async {
    final service = _FakeIssuesService();
    final action = AgentAction.fromJson(<String, dynamic>{
      'id': 14,
      'issue_id': 5,
      'run_id': 9,
      'kind': 'delete_media_files',
      'params': {
        'media_type': 'tv',
        'tmdb_id': 615,
        'season': 11,
        'episodes': [1, 2, 3],
        'blocklist': true,
      },
      'rationale': 'impossible files',
      'status': 'proposed',
      'can_decide': true,
      'issue_title': 'Futurama',
      'issue_media_type': 'tv',
      'issue_status': 'awaiting_approval',
      'instance_id': 'inst-1',
      'instance_name': 'TV',
      'instance_service_type': 'sonarr',
      'created_at': '2026-06-23T10:00:00Z',
    });
    await _pump(tester, auth: _adminState, service: service, action: action);

    expect(find.text('E1'), findsOneWidget);
    await tester.tap(find.text('E2'));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.tap(find.widgetWithText(ElevatedButton, 'Approve'));
    await tester.pumpAndSettle();
    // Confirm dialog: press its approve button.
    await tester.tap(find.byType(ElevatedButton).last);
    await tester.pumpAndSettle();

    final override = service.lastOverride as Map<String, dynamic>?;
    expect(override, isNotNull, reason: 'a shrunk selection must override');
    expect(override!['episodes'], [1, 3]);
    await _drainSnackBar(tester);
  });

  // The approver must never decide with less evidence than the model: the
  // card renders the server's remediation memory, recurrence called out, and
  // the exact media scope.
  testWidgets('prior attempts render with the recurrence verdict',
      (tester) async {
    final action = AgentAction.fromJson(<String, dynamic>{
      'id': 13,
      'issue_id': 5,
      'run_id': 9,
      'kind': 'remediate_queue',
      'params': {
        'media_type': 'tv',
        'queue_id': 4,
        'action': 'blocklist_search',
      },
      'rationale': 'try again',
      'status': 'proposed',
      'can_decide': true,
      'issue_title': 'Loop Show',
      'issue_media_type': 'tv',
      'issue_status': 'awaiting_approval',
      'instance_id': 'inst-1',
      'instance_name': 'TV',
      'instance_service_type': 'sonarr',
      'created_at': '2026-06-23T10:00:00Z',
      'issue_season': 2,
      'issue_episode': 3,
      'issue_occurrences': 3,
      'prior_attempts': [
        {
          'kind': 'remediate_queue',
          'facet': 'blocklist_search',
          'executed_at': '2026-06-23T09:00:00Z',
          'recurred': true,
        },
      ],
    });
    await _pump(
      tester,
      auth: _adminState,
      service: _FakeIssuesService(),
      action: action,
    );
    expect(find.textContaining('came back'), findsOneWidget);
    expect(find.textContaining('did not hold'), findsOneWidget);
    expect(find.textContaining('S02E03'), findsOneWidget);
    expect(find.textContaining('seen 3 times'), findsOneWidget);
  });
}

/// A manual-import proposal carrying the server's standing-rule offer.
AgentAction _proposedWithOffer({bool reactivates = false}) =>
    AgentAction.fromJson({
      'id': 20,
      'issue_id': 5,
      'run_id': 9,
      'kind': 'manual_import',
      'params': {'media_type': 'movie', 'queue_id': 7},
      'rationale': 'The download finished but Radarr needs a manual import.',
      'status': 'proposed',
      'can_decide': true,
      'issue_status': 'awaiting_approval',
      'issue_title': 'The Movie',
      'issue_media_type': 'movie',
      'instance_id': 'radarr-main',
      'instance_name': 'Main Movies',
      'instance_service_type': 'radarr',
      'auto_approval_offer': {
        'problem_kind': 'Waiting to import',
        'action_kind': 'manual_import',
        'action_facet': '',
        'label': 'Manual import · Waiting to import',
        'reactivates_paused_rule': reactivates,
      },
    });

// The approver must never decide with less evidence than the model: the card
// renders the server's remediation memory, with recurrence called out.
void main2() {}

