import 'dart:convert';
import 'dart:io';

import 'package:cantinarr/core/storage/locale_preferences.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/ui/widgets/book_status.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/widgets/season_availability.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('French catalog covers every English message and its parameters', () {
    final english = jsonDecode(File('lib/l10n/app_en.arb').readAsStringSync())
        as Map<String, dynamic>;
    final french = jsonDecode(File('lib/l10n/app_fr.arb').readAsStringSync())
        as Map<String, dynamic>;
    Set<String> messages(Map<String, dynamic> catalog) =>
        catalog.keys.where((key) => !key.startsWith('@')).toSet();
    expect(messages(french), messages(english));
    for (final key in messages(english)) {
      final translated = french[key] as String;
      expect(translated.trim(), isNotEmpty, reason: key);
      final metadata = english['@$key'] as Map<String, dynamic>?;
      final placeholders = metadata?['placeholders'] as Map<String, dynamic>?;
      for (final parameter in placeholders?.keys ?? <String>[]) {
        expect(translated, contains('{$parameter'), reason: '$key: $parameter');
      }
    }
  });

  test('restores French and normalizes an explicit regional language',
      () async {
    SharedPreferences.setMockInitialValues({'app_language': 'fr'});
    final container = ProviderContainer();
    addTearDown(container.dispose);
    container.read(appLocaleProvider);
    await Future<void>.delayed(Duration.zero);
    expect(container.read(appLocaleProvider), const Locale('fr'));
    await container
        .read(appLocaleProvider.notifier)
        .setLocale(const Locale('en', 'GB'));
    expect(container.read(appLocaleProvider), const Locale('en'));
    expect((await SharedPreferences.getInstance()).getString('app_language'),
        'en');
  });

  test('an immediate selection wins over preference restoration', () async {
    SharedPreferences.setMockInitialValues({'app_language': 'fr'});
    final notifier = AppLocaleNotifier();
    addTearDown(notifier.dispose);
    await notifier.setLocale(const Locale('en'));
    expect(notifier.state, const Locale('en'));
    expect((await SharedPreferences.getInstance()).getString('app_language'),
        'en');
  });

  test('rapid changes persist in order and system default clears the override',
      () async {
    final notifier = AppLocaleNotifier();
    addTearDown(notifier.dispose);
    await Future.wait([
      notifier.setLocale(const Locale('fr')),
      notifier.setLocale(const Locale('en')),
      notifier.setLocale(null),
    ]);
    expect(notifier.state, isNull);
    expect((await SharedPreferences.getInstance()).containsKey('app_language'),
        isFalse);
    expect(() => notifier.setLocale(const Locale('de')), throwsArgumentError);
    final restored = AppLocaleNotifier();
    addTearDown(restored.dispose);
    await Future<void>.delayed(Duration.zero);
    expect(restored.state, isNull);
  });

  test('unknown saved languages follow the system', () async {
    SharedPreferences.setMockInitialValues({'app_language': 'xx'});
    final notifier = AppLocaleNotifier();
    addTearDown(notifier.dispose);
    await Future<void>.delayed(Duration.zero);
    expect(notifier.state, isNull);
  });

  testWidgets(
      'French Canadian device uses French, explicit English switches live',
      (tester) async {
    tester.platformDispatcher.localesTestValue = const [Locale('fr', 'CA')];
    addTearDown(tester.platformDispatcher.clearLocalesTestValue);
    final container = ProviderContainer();
    addTearDown(container.dispose);
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const _LocaleHarness(),
    ));
    await tester.pumpAndSettle();
    expect(find.text('Paramètres'), findsOneWidget);
    expect(find.text('Fermer'), findsOneWidget);
    expect(find.text('fr'), findsOneWidget);
    await container
        .read(appLocaleProvider.notifier)
        .setLocale(const Locale('en'));
    await tester.pumpAndSettle();
    expect(find.text('Settings'), findsOneWidget);
    expect(find.text('Close'), findsOneWidget);
    await container.read(appLocaleProvider.notifier).setLocale(null);
    await tester.pumpAndSettle();
    expect(find.text('Paramètres'), findsOneWidget);
  });

  testWidgets('unsupported system language falls back to English',
      (tester) async {
    tester.platformDispatcher.localesTestValue = const [Locale('de', 'DE')];
    addTearDown(tester.platformDispatcher.clearLocalesTestValue);
    await tester.pumpWidget(const ProviderScope(child: _LocaleHarness()));
    await tester.pumpAndSettle();
    expect(find.text('Settings'), findsOneWidget);
    expect(find.text('en'), findsOneWidget);
  });

  testWidgets('standalone widgets retain English without an app delegate',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Builder(
      builder: (context) => Text(context.l10n.appSettings),
    )));
    expect(find.text('Settings'), findsOneWidget);
  });

  test('French book labels preserve availability and request classification',
      () {
    final french = lookupAppLocalizations(const Locale('fr'));
    final available = ChaptarrBook.fromJson({
      'id': 1,
      'title': 'Available',
      'mediaType': 'ebook',
      'monitored': true,
      'statistics': {'bookFileCount': 1},
    });
    final requested = ChaptarrBook.fromJson({
      'id': 2,
      'title': 'Available',
      'mediaType': 'audiobook',
      'monitored': true,
    });
    final englishStatus = groupedBookStatusLine([available, requested]);
    final frenchStatus =
        groupedBookStatusLine([available, requested], strings: french);
    expect(frenchStatus.color, englishStatus.color);
    expect(frenchStatus.text, contains('Demandé'));
    expect(available.title, 'Available');
  });

  test('French season availability retains counts and warning color', () {
    final stats = SonarrStatistics.fromJson({
      'episodeFileCount': 2,
      'episodeCount': 3,
      'totalEpisodeCount': 4,
    });
    final english = seasonAvailabilityLine(stats, moreToCome: true);
    final french = seasonAvailabilityLine(stats,
        moreToCome: true, strings: lookupAppLocalizations(const Locale('fr')));
    expect(french.color, english.color);
    expect(french.text.replaceAll('\u00a0', ' '),
        contains('2/3 épisodes disponibles'));
    expect(french.text.replaceAll('\u00a0', ' '), contains('1 manquant'));
    expect(french.text.replaceAll('\u00a0', ' '), contains('1 non diffusé'));
  });
}

class _LocaleHarness extends ConsumerWidget {
  const _LocaleHarness();

  @override
  Widget build(BuildContext context, WidgetRef ref) => MaterialApp(
        locale: ref.watch(appLocaleProvider),
        supportedLocales: AppLocalizations.supportedLocales,
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        home: Builder(
            builder: (context) => Scaffold(
                    body: Column(children: [
                  Text(context.l10n.appSettings),
                  Text(MaterialLocalizations.of(context).closeButtonLabel),
                  Text(Localizations.localeOf(context).languageCode),
                ]))),
      );
}
