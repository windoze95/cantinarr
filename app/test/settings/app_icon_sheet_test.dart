import 'dart:async';

import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/settings/data/app_icon_service.dart';
import 'package:cantinarr/features/settings/data/settings_search_index.dart';
import 'package:cantinarr/features/settings/ui/app_icon_sheet.dart';
import 'package:cantinarr/l10n/app_localizations.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late String current;
  late List<String> selections;
  bool failChange = false;
  bool supported = true;
  Completer<void>? pending;

  setUp(() {
    current = 'play-sun';
    selections = [];
    failChange = false;
    supported = true;
    pending = null;
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(AppIconService.channel, (call) async {
      switch (call.method) {
        case 'isSupported':
          return supported;
        case 'getIcon':
          return current;
        case 'setIcon':
          final choice = (call.arguments as Map)['icon'] as String;
          selections.add(choice);
          if (pending != null) await pending!.future;
          if (failChange) throw PlatformException(code: 'icon_change_failed');
          current = choice;
          return null;
      }
      throw MissingPluginException();
    });
  });

  tearDown(() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(AppIconService.channel, null);
  });

  Future<void> pump(WidgetTester tester,
      {Locale? locale, double scale = 1}) async {
    await tester.pumpWidget(MaterialApp(
      theme: AppTheme.dark,
      locale: locale,
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      supportedLocales: AppLocalizations.supportedLocales,
      home: Scaffold(
          body: MediaQuery(
        data: MediaQueryData(textScaler: TextScaler.linear(scale)),
        child: const AppIconSheet(),
      )),
    ));
    await tester.pumpAndSettle();
  }

  bool selected(WidgetTester tester, String label) => tester
      .widget<ListTile>(
        find.ancestor(of: find.text(label), matching: find.byType(ListTile)),
      )
      .selected;

  testWidgets(
      'loads OS choice, switches every alternative and restores default',
      (tester) async {
    current = 'original';
    await pump(tester);
    expect(selected(tester, 'Original'), isTrue);
    for (final icon in [
      AppIcon.playSign,
      AppIcon.playDome,
      AppIcon.original,
      AppIcon.playSun
    ]) {
      await tester.tap(find.text(icon.label));
      await tester.pumpAndSettle();
      expect(current, icon.id);
      expect(selected(tester, icon.label), isTrue);
    }
    expect(selections, ['play-sign', 'play-dome', 'original', 'play-sun']);
    await tester.tap(find.text('Play Sun'));
    await tester.pumpAndSettle();
    expect(selections.length, 4);
  });

  testWidgets('failed OS changes keep the actual choice and show an error',
      (tester) async {
    await pump(tester);
    failChange = true;
    await tester.tap(find.text('Play Dome'));
    await tester.pumpAndSettle();
    expect(selected(tester, 'Play Sun'), isTrue);
    expect(
        find.text('Could not update the app icon. Try again.'), findsOneWidget);
    failChange = false;
    await tester.tap(find.text('Original'));
    await tester.pumpAndSettle();
    expect(selected(tester, 'Original'), isTrue);
  });

  testWidgets('does not allow overlapping native changes', (tester) async {
    await pump(tester);
    pending = Completer<void>();
    await tester.tap(find.text('Play Sign'));
    await tester.pump();
    await tester.tap(find.text('Play Dome'));
    await tester.pump();
    expect(selections, ['play-sign']);
    pending!.complete();
    await tester.pumpAndSettle();
    expect(selected(tester, 'Play Sign'), isTrue);
  });

  testWidgets('unsupported devices cannot submit changes', (tester) async {
    supported = false;
    await pump(tester);
    expect(find.byType(ListTile), findsNothing);
    expect(find.text('This device does not support alternate app icons.'),
        findsOneWidget);
    expect(selections, isEmpty);
  });

  testWidgets('French picker fits a narrow screen with enlarged text',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);
    await pump(tester, locale: const Locale('fr'), scale: 1.6);
    expect(find.text('Originale'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  test('icon search is visible only when the native picker is available', () {
    final entry =
        settingsSearchIndex.singleWhere((entry) => entry.id == 'root.app-icon');
    expect(entry.gate(const SettingsSearchGates(user: null)), isFalse);
    expect(
        entry
            .gate(const SettingsSearchGates(user: null, appIconsVisible: true)),
        isTrue);
  });
}
