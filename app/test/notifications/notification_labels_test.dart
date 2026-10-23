import 'package:cantinarr/features/notifications/push_service.dart';
import 'package:cantinarr/features/notifications/ui/notification_labels.dart';
import 'package:cantinarr/l10n/app_localizations.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('French push diagnostics preserve counts and recipient',
      (tester) async {
    late BuildContext context;
    await tester.pumpWidget(MaterialApp(
      locale: const Locale('fr'),
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      supportedLocales: AppLocalizations.supportedLocales,
      home: Builder(builder: (value) {
        context = value;
        return const SizedBox.shrink();
      }),
    ));
    await tester.pumpAndSettle();
    expect(
        localizedDescribePushTest(context,
            const PushTestResult(tokens: 2, sent: 2, failed: 0, results: []),
            username: 'alice'),
        'Test envoyé à alice (2 appareils).');
    expect(
        localizedDescribePushTest(context,
            const PushTestResult(tokens: 1, sent: 1, failed: 0, results: [])),
        'Test envoyé à 1 appareil.');
    expect(
        localizedDescribePushTest(context,
            const PushTestResult(tokens: 0, sent: 0, failed: 0, results: [])),
        contains('Aucun de vos appareils'));
  });

  testWidgets('French push diagnostics explain provider rejection',
      (tester) async {
    late BuildContext context;
    await tester.pumpWidget(MaterialApp(
      locale: const Locale('fr'),
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      supportedLocales: AppLocalizations.supportedLocales,
      home: Builder(builder: (value) {
        context = value;
        return const SizedBox.shrink();
      }),
    ));
    await tester.pumpAndSettle();
    final message = localizedDescribePushTest(
        context,
        const PushTestResult(tokens: 1, sent: 0, failed: 1, results: [
          PushTestDeviceResult(ok: false, pruned: true, error: 'UNREGISTERED'),
        ]),
        username: 'alice');
    expect(message, contains('alice : échec de l’envoi à 1 appareil'));
    expect(message, contains('Google indique que le jeton n’est plus valide'));
  });
}
