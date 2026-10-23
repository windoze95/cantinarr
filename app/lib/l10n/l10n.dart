import 'package:flutter/widgets.dart';

import 'app_localizations.dart';

export 'app_localizations.dart';

/// Read translations from the current widget tree. Standalone widgets hosted
/// without the app's delegate retain the original English presentation.
extension AppLocalizationContext on BuildContext {
  AppLocalizations get l10n =>
      Localizations.of<AppLocalizations>(this, AppLocalizations) ??
      lookupAppLocalizations(const Locale('en'));
}

/// intl includes en_US date symbols before locale delegates load. Use that
/// canonical English locale for standalone widgets as well as the app.
extension AppLocalizationDates on AppLocalizations {
  String get dateLocale => localeName == 'en' ? 'en_US' : localeName;
}
