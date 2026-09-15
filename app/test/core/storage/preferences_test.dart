import 'package:cantinarr/core/storage/preferences.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

typedef _MenuPreferenceProvider
    = StateNotifierProvider<ConditionalMenuVisibilityNotifier, bool>;

final _menuPreferences = <({
  String label,
  _MenuPreferenceProvider provider,
})>[
  (
    label: 'Approvals conditional visibility',
    provider: approvalsMenuOnlyWhenPendingProvider,
  ),
  (
    label: 'Issues conditional visibility',
    provider: issuesMenuOnlyWhenActiveProvider,
  ),
  (
    label: 'Agent fixes conditional visibility',
    provider: agentFixesMenuOnlyWhenAwaitingReviewProvider,
  ),
  (
    label: 'Profile approvals conditional visibility',
    provider: profileApprovalsMenuOnlyWhenPendingProvider,
  ),
];

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  for (final preference in _menuPreferences) {
    test('${preference.label} persists across provider containers', () async {
      final first = ProviderContainer();
      addTearDown(first.dispose);

      expect(first.read(preference.provider), isTrue,
          reason: 'empty entries must not flash before preferences load');
      await pumpEventQueue();
      expect(first.read(preference.provider), isTrue);

      await first.read(preference.provider.notifier).set(false);
      expect(first.read(preference.provider), isFalse);

      final restored = ProviderContainer();
      addTearDown(restored.dispose);
      restored.read(preference.provider);

      await _waitFor(
        () => !restored.read(preference.provider),
      );
      expect(restored.read(preference.provider), isFalse);
    });
  }

  test('conditional menu preferences update independently', () async {
    SharedPreferences.setMockInitialValues({
      'approvals_menu_only_when_pending': false,
      'issues_menu_only_when_active': false,
      'agent_fixes_menu_only_when_awaiting_review': false,
      'profile_approvals_menu_only_when_pending': false,
    });
    final container = ProviderContainer();
    addTearDown(container.dispose);

    for (final preference in _menuPreferences) {
      container.read(preference.provider);
    }
    await pumpEventQueue();

    await container
        .read(approvalsMenuOnlyWhenPendingProvider.notifier)
        .set(true);
    expect(container.read(approvalsMenuOnlyWhenPendingProvider), isTrue);
    expect(container.read(issuesMenuOnlyWhenActiveProvider), isFalse);
    expect(
      container.read(agentFixesMenuOnlyWhenAwaitingReviewProvider),
      isFalse,
    );

    await container.read(issuesMenuOnlyWhenActiveProvider.notifier).set(true);
    await container
        .read(approvalsMenuOnlyWhenPendingProvider.notifier)
        .set(false);
    expect(container.read(approvalsMenuOnlyWhenPendingProvider), isFalse);
    expect(container.read(issuesMenuOnlyWhenActiveProvider), isTrue);
    expect(
      container.read(agentFixesMenuOnlyWhenAwaitingReviewProvider),
      isFalse,
    );

    await container
        .read(agentFixesMenuOnlyWhenAwaitingReviewProvider.notifier)
        .set(true);
    await container.read(issuesMenuOnlyWhenActiveProvider.notifier).set(false);
    expect(container.read(approvalsMenuOnlyWhenPendingProvider), isFalse);
    expect(container.read(issuesMenuOnlyWhenActiveProvider), isFalse);
    expect(
      container.read(agentFixesMenuOnlyWhenAwaitingReviewProvider),
      isTrue,
    );

    final restored = ProviderContainer();
    addTearDown(restored.dispose);
    for (final preference in _menuPreferences) {
      restored.read(preference.provider);
    }
    await _waitFor(
      () => restored.read(agentFixesMenuOnlyWhenAwaitingReviewProvider),
    );

    expect(restored.read(approvalsMenuOnlyWhenPendingProvider), isFalse);
    expect(restored.read(issuesMenuOnlyWhenActiveProvider), isFalse);
    expect(
      restored.read(agentFixesMenuOnlyWhenAwaitingReviewProvider),
      isTrue,
    );
  });
}

Future<void> _waitFor(bool Function() condition) async {
  final deadline = DateTime.now().add(const Duration(seconds: 2));
  while (!condition() && DateTime.now().isBefore(deadline)) {
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
  expect(condition(), isTrue);
}
