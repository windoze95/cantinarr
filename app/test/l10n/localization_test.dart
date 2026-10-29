import 'dart:convert';
import 'dart:io';

import 'package:cantinarr/core/storage/locale_preferences.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/ui/widgets/book_status.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/widgets/season_availability.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:cantinarr/l10n/app_languages.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('every ARB covers the template, descriptors, and parameters', () {
    final english = jsonDecode(File('lib/l10n/app_en.arb').readAsStringSync())
        as Map<String, dynamic>;
    Set<String> messages(Map<String, dynamic> catalog) =>
        catalog.keys.where((key) => !key.startsWith('@')).toSet();
    final files = Directory('lib/l10n').listSync().whereType<File>().where(
        (file) => RegExp(r'app_[a-zA-Z_0-9]+\.arb$').hasMatch(file.path));
    expect(files, isNotEmpty);
    for (final file in files) {
      final catalog =
          jsonDecode(file.readAsStringSync()) as Map<String, dynamic>;
      final locale = catalog['@@locale'] as String;
      expect(file.uri.pathSegments.last, 'app_$locale.arb');
      final entry = appLanguageForTag(locale);
      expect(entry, isNotNull, reason: file.path);
      expect(entry!.tag, locale.replaceAll('_', '-'), reason: file.path);
      expect(GlobalMaterialLocalizations.delegate.isSupported(entry.locale),
          isTrue,
          reason: '$locale needs Flutter Material translations');
      expect(GlobalCupertinoLocalizations.delegate.isSupported(entry.locale),
          isTrue,
          reason: '$locale needs Flutter Cupertino translations');
      expect(
          GlobalWidgetsLocalizations.delegate.isSupported(entry.locale), isTrue,
          reason: '$locale needs Flutter widget translations');
      expect(catalog['languageNativeName'], entry.nativeName);
      expect(catalog['metadataLanguageTag'], entry.metadataTag);
      expect(
          entry.metadataTag.isEmpty ||
              RegExp(r'^[a-z]{2}(?:-[A-Z]{2})?$').hasMatch(entry.metadataTag),
          isTrue,
          reason:
              '$locale metadataLanguageTag must be a provider language/country tag or empty');
      expect(messages(catalog), messages(english), reason: file.path);
      for (final key in messages(english)) {
        final translated = catalog[key] as String;
        if (key != 'metadataLanguageTag') {
          expect(translated.trim(), isNotEmpty, reason: '$locale: $key');
        }
        final metadata = english['@$key'] as Map<String, dynamic>?;
        final placeholders = metadata?['placeholders'] as Map<String, dynamic>?;
        for (final parameter in placeholders?.keys ?? <String>[]) {
          expect(
              RegExp('\\{\\s*${RegExp.escape(parameter)}(?:\\s*[,}])')
                  .hasMatch(translated),
              isTrue,
              reason: '$locale: $key must preserve {$parameter}');
        }
      }
    }
  });

  test(
      'language registry uses generated labels and rejects malformed saved tags',
      () {
    expect(AppLocalizations.supportedLocales.first, const Locale('en'));
    expect(appLanguages.map((entry) => entry.locale),
        AppLocalizations.supportedLocales);
    expect(appLanguageForTag('en')?.nativeName, 'English');
    expect(appLanguageForTag('fr')?.nativeName, 'Français');
    expect(metadataLanguageNames['fr-FR'], 'Français');
    for (final tag in ['xx', 'fr-extra-private', 'fr--CA', 'FR', 'fr/CA']) {
      expect(appLanguageForTag(tag), isNull, reason: tag);
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
    final expected =
        AppLocalizations.supportedLocales.contains(const Locale('en', 'GB'))
            ? const Locale('en', 'GB')
            : const Locale('en');
    expect(container.read(appLocaleProvider), expected);
    expect((await SharedPreferences.getInstance()).getString('app_language'),
        expected.toLanguageTag());
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
    expect(() => notifier.setLocale(const Locale('zz')), throwsArgumentError);
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

  testWidgets('French device uses French, explicit English switches live',
      (tester) async {
    tester.platformDispatcher.localesTestValue = const [Locale('fr')];
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
    tester.platformDispatcher.localesTestValue = const [Locale('zz', 'ZZ')];
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
                  Text(Localizations.localeOf(context).toLanguageTag()),
                ]))),
      );
}
