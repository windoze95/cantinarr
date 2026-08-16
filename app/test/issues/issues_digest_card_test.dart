import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/issues/data/issue_models.dart';
import 'package:cantinarr/features/issues/data/issues_service.dart';
import 'package:cantinarr/features/issues/logic/issues_provider.dart';
import 'package:cantinarr/features/issues/ui/issues_list_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// The scoreboard belongs at the head of the list it summarises, and its
/// numbers must survive a narrow card: a count may never be orphaned from the
/// word it counts, so the only place a line may wrap is before a separator.
///
/// "Resolved" is OUTCOME vocabulary — every problem that ended well — with
/// attribution glued to the number, so both admin readings survive: the total
/// honors the week, the lanes keep automation from claiming churn.
void main() {
  testWidgets('digest card renders outcome-first with unbreakable stats',
      (tester) async {
    final service = _FakeIssuesService(
      issues: [_issue(1, 'needs_admin')],
      digest: const {
        'issues_resolved': 4,
        'self_cleared': 37,
        'resolved_by_agent': 2,
        'rule_approved': 1,
        'resolved_by_admin': 1,
        'closed_no_fix': 2,
        'dismissed': 5,
        'needs_admin_open': 2,
        'paused_rules': 1,
      },
    );
    await _pump(tester, service);

    final card = _clauses(tester);
    expect(card.window, isNotEmpty, reason: 'the digest card should render');

    // The headline is the outcome total (4 + 37), attribution glued behind an
    // em dash that follows the separator wrap policy: breakable before, glued
    // after. Every stat is internally non-breaking.
    expect(
      card.window,
      'Last\u00A07\u00A0days: 41\u00A0resolved'
      ' \u2014\u00A02\u00A0by\u00A0the\u00A0agent'
      ' \u00B7\u00A01\u00A0by\u00A0your\u00A0rules'
      ' \u00B7\u00A01\u00A0by\u00A0you'
      ' \u00B7\u00A037\u00A0on\u00A0their\u00A0own'
      ' \u00B7\u00A02\u00A0closed\u00A0by\u00A0you\u00A0(no\u00A0fix)'
      ' \u00B7\u00A05\u00A0dismissed',
    );
    // The delimiter leads the next line: breakable space BEFORE dot and dash,
    // glued space after.
    expect(card.window, isNot(contains('\u00B7 ')));
    expect(card.window, isNot(contains('\u2014 ')));

    // Open work is state right now, not something the last 7 days did.
    expect(card.now, contains('2\u00A0need\u00A0you'));
    expect(card.now, contains('1\u00A0rule\u00A0paused'));
    expect(card.window, isNot(contains('need')));
    expect(card.window, isNot(contains('paused')));
  });

  // The live week that motivated outcome-first: 438 problems ended well, all
  // by themselves, and the two hand-closures stay visible without being called
  // resolved — the closer's own verb was "Close without fix".
  testWidgets('a self-clearing week is resolved, attributed to no one',
      (tester) async {
    final service = _FakeIssuesService(
      issues: const [],
      digest: const {
        'issues_resolved': 0,
        'self_cleared': 438,
        'rule_approved': 0,
        'resolved_by_agent': 0,
        'resolved_by_admin': 0,
        'closed_no_fix': 2,
        'dismissed': 5,
        'needs_admin_open': 0,
        'paused_rules': 1,
      },
    );
    await _pump(tester, service);

    final card = _clauses(tester);
    expect(
      card.window,
      'Last\u00A07\u00A0days: 438\u00A0resolved'
      ' \u2014\u00A0all\u00A0on\u00A0their\u00A0own'
      ' \u00B7\u00A02\u00A0closed\u00A0by\u00A0you\u00A0(no\u00A0fix)'
      ' \u00B7\u00A05\u00A0dismissed',
    );
    expect(card.now, contains('1\u00A0rule\u00A0paused'));
  });

  testWidgets('grammar: lone counts read singular', (tester) async {
    final service = _FakeIssuesService(
      issues: [_issue(1, 'needs_admin')],
      digest: const {
        'issues_resolved': 0,
        'self_cleared': 1,
        'closed_no_fix': 1,
        'needs_admin_open': 1,
        'paused_rules': 3,
      },
    );
    await _pump(tester, service);

    final card = _clauses(tester);
    // A lone unattributed resolution resolved on ITS own.
    expect(card.window, contains('1\u00A0resolved \u2014\u00A0on\u00A0its\u00A0own'));
    expect(card.window, contains('1\u00A0closed\u00A0by\u00A0you\u00A0(no\u00A0fix)'));
    // "1 needs you", not "1 need you"; three rules pluralise.
    expect(card.now, contains('1\u00A0needs\u00A0you'));
    expect(card.now, contains('3\u00A0rules\u00A0paused'));
  });

  testWidgets('a truly empty week claims nothing and the now clause is absent',
      (tester) async {
    final service = _FakeIssuesService(
      issues: const [],
      digest: const {'issues_resolved': 0, 'self_cleared': 0},
    );
    await _pump(tester, service);

    expect(_clauses(tester).window, 'Last\u00A07\u00A0days: 0\u00A0resolved');
    expect(find.textContaining('Right'), findsNothing);
  });

  testWidgets('closed tab says how much history it is not showing',
      (tester) async {
    final service = _FakeIssuesService(
      issues: [_issue(1, 'resolved'), _issue(2, 'resolved')],
      closedTotal: 667,
    );
    await _pump(tester, service);

    await tester.tap(find.text('Closed'));
    await tester.pumpAndSettle();

    expect(
      find.text('Showing the 2 most recent of 667 closed issues.'),
      findsOneWidget,
    );
  });

  testWidgets('no note when the closed list is complete', (tester) async {
    final service = _FakeIssuesService(
      issues: [_issue(1, 'resolved')],
      closedTotal: 1,
    );
    await _pump(tester, service);

    await tester.tap(find.text('Closed'));
    await tester.pumpAndSettle();

    expect(find.textContaining('most recent of'), findsNothing);
  });
}

/// The card's two clauses: what the window did, and what is true right now.
({String window, String now}) _clauses(WidgetTester tester) {
  final lines = tester
      .widgetList<Text>(find.byType(Text))
      .map((t) => t.data ?? '')
      .toList();
  String clause(String head) =>
      lines.firstWhere((d) => d.startsWith(head), orElse: () => '');
  return (window: clause('Last'), now: clause('Right'));
}

Future<void> _pump(WidgetTester tester, _FakeIssuesService service) async {
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
      child: const MaterialApp(home: IssuesListScreen()),
    ),
  );
  await tester.pumpAndSettle();
}

Issue _issue(int id, String status) => Issue.fromJson({
      'id': id,
      'source': 'auto',
      'status': status,
      'media_type': 'tv',
      'title': 'Example Show',
      'detail': 'detail',
      'occurrences': 1,
      'read': true,
      'created_at': '2026-07-10T10:00:00Z',
      'updated_at': '2026-07-10T10:00:00Z',
      if (status == 'resolved') 'closed_at': '2026-07-10T11:00:00Z',
    });

const _adminState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(),
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => _adminState;
}

class _FakeIssuesService extends IssuesService {
  _FakeIssuesService({
    this.issues = const [],
    this.digest,
    this.closedTotal = 0,
  }) : super(backendDio: Dio());

  List<Issue> issues;
  Map<String, dynamic>? digest;
  int closedTotal;

  @override
  Future<IssuePage> listIssues({String? status}) async =>
      IssuePage(issues: issues, closedTotal: closedTotal);

  @override
  Future<Map<String, dynamic>> agentDigest({int days = 7}) async {
    final d = digest;
    if (d == null) throw StateError('no digest');
    return d;
  }
}
