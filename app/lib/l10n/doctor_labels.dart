import 'package:flutter/widgets.dart';

import 'l10n.dart';

/// Translates diagnosis copy after classification; rule prefixes and raw
/// provider messages retain their original values.
String localizeDoctorLabel(BuildContext context, String label) {
  final detail = RegExp(
          r"^This download errored \((.*)\) and won't recover on its own\.$",
          dotAll: true)
      .firstMatch(label);
  if (detail != null)
    return context.l10n.doctorDownloadErrorDetail(detail.group(1)!);
  return switch (label) {
    'Dangerous file in release' => context.l10n.doctorDangerousFileInRelease,
    'This release contains an executable or otherwise dangerous file — possible malware. Do not import it.' =>
      context.l10n.doctorThisReleaseContainsAnExecutableOr,
    'TheXEM mapping needs confirmation' =>
      context.l10n.doctorTheXEMMappingNeedsConfirmation,
    'This show has individual episode mappings on TheXEM, so the download wasn\'t imported automatically. Confirm the episode and we\'ll force the import.' =>
      context.l10n.doctorThisShowHasIndividualEpisodeMappings,
    'Release is an unextracted archive' =>
      context.l10n.doctorReleaseIsAnUnextractedArchive,
    'This release is packed in an archive (RAR) and must be unpacked before it can be imported.' =>
      context.l10n.doctorThisReleaseIsPackedInAn,
    'Nothing importable in the folder' =>
      context.l10n.doctorNothingImportableInTheFolder,
    'The download folder had nothing importable — likely all samples, an unextracted archive, or a path/permissions issue.' =>
      context.l10n.doctorTheDownloadFolderHadNothingImportable,
    'Could not verify sample' => context.l10n.doctorCouldNotVerifySample,
    'The file\'s runtime couldn\'t be verified, so it might be a sample clip. Force the import if you know it\'s the full file.' =>
      context.l10n.doctorTheFileSRuntimeCouldnT,
    'File looks like a sample' => context.l10n.doctorFileLooksLikeASample,
    'The file looks like a sample clip rather than the full release.' =>
      context.l10n.doctorTheFileLooksLikeASample,
    'Unsupported file type' => context.l10n.doctorUnsupportedFileType,
    'The file has an extension the service doesn\'t import as video. It\'s likely the wrong file (or needs unpacking) — remove and re-search for a proper release.' =>
      context.l10n.doctorTheFileHasAnExtensionThe,
    'Invalid video file' => context.l10n.doctorInvalidVideoFile,
    'The service flagged this as not a valid video file (for example a macOS resource-fork "._" file). Inspect the candidates and import the real file manually, or remove and re-search.' =>
      context.l10n.doctorTheServiceFlaggedThisAsNot,
    'Couldn\'t parse the file' => context.l10n.doctorCouldnTParseTheFile,
    'The service couldn\'t parse the file name to figure out what it is, so it wasn\'t imported. Map it yourself with a manual import, or remove and re-search for a cleaner release.' =>
      context.l10n.doctorTheServiceCouldnTParseThe,
    'Release contents don\'t match' =>
      context.l10n.doctorReleaseContentsDonTMatch,
    'Sonarr/Radarr expected files this release didn\'t contain, so it wasn\'t fully imported. Review the candidate files and import what\'s there manually, or remove and re-search for a complete release.' =>
      context.l10n.doctorSonarrRadarrExpectedFilesThisRelease,
    'An episode this grab was supposed to include wasn\'t in the release, so it wasn\'t fully imported. Review the candidate files and import what\'s there manually, or remove and re-search for a complete release.' =>
      context.l10n.doctorAnEpisodeThisGrabWasSupposed,
    'Episode mapping problem' => context.l10n.doctorEpisodeMappingProblem,
    'The service couldn\'t work out which season/episode this file is, so it wasn\'t imported. Map it yourself with a manual import.' =>
      context.l10n.doctorTheServiceCouldnTWorkOut,
    'Not an upgrade' => context.l10n.doctorNotAnUpgrade,
    'This release isn\'t better than the file you already have, so it wasn\'t imported.' =>
      context.l10n.doctorThisReleaseIsnTBetterThan,
    'Not a Custom Format upgrade' => context.l10n.doctorNotACustomFormatUpgrade,
    'This release doesn\'t improve on your existing file\'s Custom Format score, so it wasn\'t imported. Clear it, or force the import if you want it anyway.' =>
      context.l10n.doctorThisReleaseDoesnTImproveOn,
    'Already imported' => context.l10n.doctorAlreadyImported,
    'This exact download was already imported, so there\'s nothing to do but clear it.' =>
      context.l10n.doctorThisExactDownloadWasAlreadyImported,
    'Matched by ID — needs manual import' =>
      context.l10n.doctorMatchedByIDNeedsManualImport,
    'The release was matched to the series by its download-client ID rather than by its name, so the service won\'t import it automatically. Import it manually.' =>
      context.l10n.doctorTheReleaseWasMatchedToThe,
    'The release was matched to the movie by its download-client ID rather than by its name, so the service won\'t import it automatically. Import it manually.' =>
      context.l10n.doctorTheReleaseWasMatchedToThe31,
    'Unmonitored' => context.l10n.doctorUnmonitored,
    'This item is unmonitored, so the service won\'t grab or import it. Monitor it first if you want it.' =>
      context.l10n.doctorThisItemIsUnmonitoredSoThe,
    'Not enough free space' => context.l10n.doctorNotEnoughFreeSpace,
    'The library drive is below its free-space floor. Free up space, then rescan to retry.' =>
      context.l10n.doctorTheLibraryDriveIsBelowIts,
    'Remote path mapping' => context.l10n.doctorRemotePathMapping,
    'The download client reported a path the service can\'t reach on disk — a remote-path-mapping problem. Run get_arr_health to confirm the mapping, fix it, then rescan to retry.' =>
      context.l10n.doctorTheDownloadClientReportedAPath,
    'Path or permissions error' => context.l10n.doctorPathOrPermissionsError,
    'A permissions problem or wrong remote-path mapping blocked the import. Run get_arr_health to confirm the config, fix access to the path, then rescan to retry.' =>
      context.l10n.doctorAPermissionsProblemOrWrongRemote,
    'Path not accessible' => context.l10n.doctorPathNotAccessible,
    'The download path doesn\'t exist or isn\'t accessible to the service — usually a remote-path mapping issue. Run get_arr_health to confirm, fix the path, then rescan to retry.' =>
      context.l10n.doctorTheDownloadPathDoesnTExist,
    'Download stalled' => context.l10n.doctorDownloadStalled,
    'This torrent has no seeders and will never finish on its own.' =>
      context.l10n.doctorThisTorrentHasNoSeedersAnd,
    'This torrent has no connections and will never finish on its own.' =>
      context.l10n.doctorThisTorrentHasNoConnectionsAnd,
    'Download client can\'t fetch the magnet' =>
      context.l10n.doctorDownloadClientCanTFetchThe,
    'Your torrent client can\'t resolve this magnet link (often DHT is disabled), so the download will never start. Remove and blocklist it, then re-search — a usenet or non-magnet release avoids this. Run get_arr_health to check the client.' =>
      context.l10n.doctorYourTorrentClientCanTResolve,
    'Download client unreachable' =>
      context.l10n.doctorDownloadClientUnreachable,
    'The service can\'t reach your download client, so nothing in the queue can progress. Run get_arr_health to confirm, then fix the client connection — this isn\'t a problem with the release itself.' =>
      context.l10n.doctorTheServiceCanTReachYour,
    'Download client error' => context.l10n.doctorDownloadClientError,
    'Your download client errored on this download. It won\'t recover on its own.' =>
      context.l10n.doctorYourDownloadClientErroredOnThis,
    'Download error' => context.l10n.doctorDownloadError,
    'This download errored and won\'t recover on its own.' =>
      context.l10n.doctorThisDownloadErroredAndWonT,
    'Waiting to import' => context.l10n.doctorWaitingToImport,
    'The download finished but hasn\'t been imported yet — the import pass hasn\'t run. Process it now to import it.' =>
      context.l10n.doctorTheDownloadFinishedButHasnT,
    'Download failed' => context.l10n.doctorDownloadFailed,
    'This download failed. Remove and blocklist it so a fresh search grabs a different release.' =>
      context.l10n.doctorThisDownloadFailedRemoveAndBlocklist,
    'Import blocked' => context.l10n.doctorImportBlocked,
    'This download couldn\'t be imported automatically. Review the candidate files and import manually, or remove and re-search.' =>
      context.l10n.doctorThisDownloadCouldnTBeImported,
    _ => label,
  };
}
