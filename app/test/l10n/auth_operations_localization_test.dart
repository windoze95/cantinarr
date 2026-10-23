import 'package:cantinarr/l10n/auth_messages.dart';
import 'package:cantinarr/l10n/doctor_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:cantinarr/l10n/operations_labels.dart';
import 'package:cantinarr/l10n/shell_labels.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('English action helpers preserve existing vocabulary and case',
      (tester) async {
    late BuildContext context;
    await tester.pumpWidget(MaterialApp(home: Builder(builder: (value) {
      context = value;
      return const SizedBox.shrink();
    })));
    expect(localizeLibraryAction(context, 'Edit series'), 'Edit series');
    expect(localizeLibraryAction(context, 'Unmonitor Episode'),
        'Unmonitor Episode');
    expect(localizeLibraryNoun(context, 'series'), 'series');
    expect(localizeLibraryNoun(context, 'Episode'), 'Episode');
  });

  testWidgets('French diagnosis and auth copy preserve external details',
      (tester) async {
    late BuildContext context;
    await tester.pumpWidget(MaterialApp(
      locale: const Locale('fr'),
      supportedLocales: AppLocalizations.supportedLocales,
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      home: Builder(builder: (value) {
        context = value;
        return const SizedBox.shrink();
      }),
    ));
    await tester.pumpAndSettle();

    expect(localizeAuthMessage(context, 'Invalid username or password'),
        'Nom d’utilisateur ou mot de passe incorrect');
    expect(
        localizeAuthMessage(
            context, 'Server error (503). Check server logs for details.'),
        'Erreur du serveur (503). Consultez les journaux du serveur.');
    expect(localizeDoctorLabel(context, 'Download stalled'),
        'Téléchargement bloqué');
    expect(
        localizeDoctorLabel(context,
            "This download errored (provider (offline)) and won't recover on its own."),
        'Ce téléchargement a rencontré une erreur (provider (offline)) et ne se rétablira pas de lui-même.');
    const externalDetail = 'Upstream supplied a new diagnostic: XYZ';
    expect(localizeDoctorLabel(context, externalDetail), externalDetail);
    expect(localizeAuthMessage(context, externalDetail), externalDetail);
  });

  testWidgets('French operation labels preserve passive values',
      (tester) async {
    late BuildContext context;
    await tester.pumpWidget(MaterialApp(
      locale: const Locale('fr'),
      supportedLocales: AppLocalizations.supportedLocales,
      localizationsDelegates: AppLocalizations.localizationsDelegates,
      home: Builder(builder: (value) {
        context = value;
        return const SizedBox.shrink();
      }),
    ));
    await tester.pumpAndSettle();

    expect(localizeOperationsLabel(context, 'Season 12'), 'Saison 12');
    expect(context.l10n.opsRunSteps(1), '1 étape');
    expect(context.l10n.opsRunSteps(2), '2 étapes');
    expect(
        context.l10n.opsAttentionBatchCount(1), '1 nécessite une intervention');
    expect(context.l10n.opsDeleteEpisodeTarget(1, 'saison 2, ', '3'),
        '1 fichier de votre bibliothèque : saison 2, épisode 3');

    expect(
        localizeOperationsLabel(
            context, 'The issue is already closed, so this fix will not run.'),
        'Le problème est déjà fermé ; cette correction ne sera pas exécutée.');
    expect(localizedConfigValue(context, true), 'Activé');
    expect(localizedConfigValue(context, false), 'Désactivé');
    expect(localizedConfigValue(context, null), 'Non défini');
    // A saved string that happens to equal client vocabulary is still data.
    expect(localizedConfigValue(context, 'Enabled'), 'Enabled');
    expect(localizedConfigValue(context, 'Current'), 'Current');
    expect(localizeOperationsLabel(context, 'new_server_status'),
        'new_server_status');
  });
}
