import 'dart:async';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/issues/data/issue_models.dart';
import 'package:cantinarr/features/issues/data/issues_service.dart';
import 'package:cantinarr/features/issues/logic/issues_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

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
  _FakeIssuesService(this.issues) : super(backendDio: Dio());

  List<Issue> issues;
  int listCalls = 0;

  @override
  Future<List<Issue>> listIssues({String? status}) async {
    listCalls++;
    return issues;
  }
}

Issue _issue(int id, String status) => Issue.fromJson({
      'id': id,
      'status': status,
      'media_type': 'movie',
      'tmdb_id': id,
      'title': 'Title $id',
    });

Future<void> _waitFor(bool Function() condition) async {
  final deadline = DateTime.now().add(const Duration(seconds: 2));
  while (!condition() && DateTime.now().isBefore(deadline)) {
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
  expect(condition(), isTrue);
}

void main() {
  test('issue_updated refetch counts only actionable issues', () async {
    final events = StreamController<WsEvent>.broadcast();
    final service = _FakeIssuesService([
      _issue(1, 'open'),
      _issue(2, 'awaiting_approval'),
    ]);
    final container = ProviderContainer(
      overrides: [
        authProvider.overrideWith(_FakeAuthNotifier.new),
        issuesServiceProvider.overrideWithValue(service),
        realtimeEventsProvider.overrideWithValue(events.stream),
      ],
    );
    addTearDown(() async {
      container.dispose();
      await events.close();
    });

    await container.read(authProvider.future);
    final subscription = container.listen<int>(
      openIssuesProvider,
      (_, __) {},
      fireImmediately: true,
    );
    addTearDown(subscription.close);

    await _waitFor(() => service.listCalls == 1);
    expect(container.read(openIssuesProvider), 2);

    service.issues = [
      _issue(1, 'observing'),
      _issue(2, 'recovering'),
      _issue(3, 'open'),
    ];
    events.add(const WsEvent(
      type: 'issue_updated',
      // A legacy/open total must not override the app's attention calculation.
      data: {'issue_id': 1, 'open_count': 99},
    ));

    // listCalls increments before the async refresh publishes its derived
    // count. Wait for the observable contract rather than racing that internal
    // implementation detail.
    await _waitFor(() => container.read(openIssuesProvider) == 1);
    expect(service.listCalls, greaterThanOrEqualTo(2));
    expect(container.read(openIssuesProvider), 1);
  });
}
