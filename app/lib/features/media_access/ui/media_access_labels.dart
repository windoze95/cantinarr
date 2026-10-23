import 'package:flutter/widgets.dart';
import 'package:cantinarr/l10n/l10n.dart';

/// Translate app-generated account display labels without changing service state.
String mediaAccessLabel(BuildContext context, String label) => switch (label) {
      'Could not load accounts and their Cantinarr links. Refresh before importing; existing links are unknown.' =>
        context.l10n.mediaImportAccountsUnknown,
      'Could not verify the latest accounts. Completed results are kept. Check current state before retrying incomplete accounts.' =>
        context.l10n.mediaImportAccountsUnverified,
      'Managed by Cantinarr' => context.l10n.mediaManagedByCantinarr,
      'Linked only' => context.l10n.mediaLinkedOnly,
      'Access change pending' => context.l10n.mediaAccessChangePending,
      'Off on server' => context.l10n.mediaOffOnServer,
      'Active on server' => context.l10n.mediaActiveOnServer,
      'Protected administrator' => context.l10n.mediaProtectedAdministrator,
      'Server access unconfirmed' => context.l10n.mediaServerAccessUnconfirmed,
      'Awaiting Plex acceptance' => context.l10n.mediaAwaitingPlexAcceptance,
      _ => label,
    };
