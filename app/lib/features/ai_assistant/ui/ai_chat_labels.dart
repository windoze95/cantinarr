import 'package:flutter/foundation.dart';
import 'package:cantinarr/l10n/app_localizations.dart';

import '../data/ai_chat_capabilities.dart';

/// Localized suggestions are display copy until the person chooses to send one.
/// Existing conversation messages and provider responses are never translated.
extension LocalizedAiChatCapabilities on AiChatCapabilities {
  String localizedWelcome(AppLocalizations l10n) {
    if (l10n.localeName == 'en') return welcomeMessage;
    final actions = <String>[];
    String mediaNames(List<String> values) => _join(
        values
            .map((value) => switch (value) {
                  'movie' => l10n.settingsMovies2,
                  'tv' => l10n.settingsTvShows,
                  'book' => l10n.settingsBooks2,
                  'music' => l10n.settingsMusic2,
                  _ => value,
                })
            .toList(),
        l10n);
    if (discoverMediaTypes.isNotEmpty &&
        listEquals(discoverMediaTypes, requestMediaTypes)) {
      actions.add(l10n.settingsMessageDiscoverAndRequestValue1(
          mediaNames(discoverMediaTypes)));
    } else {
      if (discoverMediaTypes.isNotEmpty)
        actions.add(
            l10n.settingsMessageDiscoverValue1(mediaNames(discoverMediaTypes)));
      if (requestMediaTypes.isNotEmpty)
        actions.add(
            l10n.settingsMessageRequestValue12(mediaNames(requestMediaTypes)));
    }
    if (checkAvailability) actions.add(l10n.settingsCheckWhatSAvailable);
    if (browseLibraries) actions.add(l10n.settingsBrowseYourLibraries);
    if (manageDownloads) {
      actions.add(l10n.settingsManageDownloads);
    } else if (checkDownloads) {
      actions.add(l10n.settingsCheckDownloads);
    }
    if (troubleshoot) actions.add(l10n.settingsTroubleshootIssues);
    if (configureServices) actions.add(l10n.settingsConfigureConnectedServices);
    if (actions.isEmpty)
      return l10n.settingsHeyIMYourCantinarrAssistantWhatWould;
    return l10n
        .settingsMessageHeyIMYourCantinarrAssistantICan(_join(actions, l10n));
  }

  List<String> localizedSuggestions(AppLocalizations l10n) => [
        if (discoverMediaTypes.contains('movie')) l10n.settingsRecommendAMovie,
        if (discoverMediaTypes.contains('tv')) l10n.settingsFindATvShow,
        if (discoverMediaTypes.contains('book')) l10n.settingsFindABook,
        if (discoverMediaTypes.contains('music')) l10n.settingsFindAnAlbum,
        if (checkAvailability) l10n.settingsWhatSAvailableInMyLibrary,
        if (troubleshoot) l10n.settingsCheckForDownloadProblems,
        if (configureServices) l10n.settingsHelpConfigureMyServices,
      ].take(4).toList();

  static String _join(List<String> values, AppLocalizations l10n) =>
      switch (values.length) {
        0 => '',
        1 => values.single,
        2 => l10n.settingsMessageValue1AndValue2(values.first, values.last),
        _ => l10n.settingsMessageValue1AndValue2(
            values.take(values.length - 1).join(', '), values.last),
      };
}
