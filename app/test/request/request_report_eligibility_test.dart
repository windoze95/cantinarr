import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:cantinarr/features/request/data/tv_match_service.dart';
import 'package:cantinarr/features/request/logic/request_provider.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final status in RequestStatus.values) {
    test('${status.name} reporting follows accepted state, not a tap', () {
      final expected = status != RequestStatus.unavailable &&
          status != RequestStatus.denied;
      expect(RequestState(status: status, hasStatus: true).canReportProblem, expected);
      // A refresh does not hide a report for an already accepted request.
      expect(RequestState(status: status, isCheckingStatus: true).canReportProblem, expected);
    });
  }

  test('initial loading, missing status and blank errors are not failures', () {
    expect(const RequestState().canReportProblem, isFalse);
    expect(const RequestState(isCheckingStatus: true).canReportProblem, isFalse);
    expect(const RequestState(isRequesting: true).canReportProblem, isFalse);
    expect(const RequestState(error: '  ').canReportProblem, isFalse);
  });

  test('finished status and submission failures can be reported', () {
    expect(const RequestState(error: 'Could not check status').canReportProblem, isTrue);
    expect(const RequestState(hasStatus: true, error: 'Request failed').canReportProblem, isTrue);
    expect(const RequestState(error: 'Old failure', isCheckingStatus: true).canReportProblem, isFalse);
    expect(const RequestState(error: 'Old failure', isRequesting: true).canReportProblem, isFalse);
    expect(const RequestState(error: 'Request failed')
        .copyWith(hasStatus: true).canReportProblem, isFalse);
  });

  for (final state in ['unresolved', 'paused', 'resolved']) {
    test('$state TV match without an error message', () {
      final match = TVMatch(tmdbId: 299939, tvdbId: 0, title: 'Lizzie Borden',
        targetTitle: '', provenance: 'default', revision: 'v1',
        state: state, seasonMap: const {});
      expect(RequestState(match: match).canReportProblem, state != 'resolved');
      expect(RequestState(match: match, isCheckingStatus: true).canReportProblem, isFalse);
    });
  }
}
