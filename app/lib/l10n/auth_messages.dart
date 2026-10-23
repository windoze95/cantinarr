import 'package:flutter/widgets.dart';

import 'l10n.dart';

/// Localizes application-owned auth errors at the presentation boundary.
/// Unrecognized server and credential-provider details are preserved.
String localizeAuthMessage(BuildContext context, String message) {
  final normalized = message.replaceFirst(
      RegExp(r'^(?:Exception|Bad state|FormatException): '), '');
  final status =
      RegExp(r'^Server error \((\d+)\)\. Check server logs for details\.$')
          .firstMatch(normalized);
  if (status != null) return context.l10n.authErrorServerCode(status.group(1)!);
  return switch (normalized) {
    'Invalid username or password' => context.l10n.authErrorInvalidCredentials,
    'Server not found at this URL' => context.l10n.authErrorServerNotFound,
    'Username already taken' => context.l10n.authErrorUsernameTaken,
    'Too many attempts. Please wait a moment and try again.' =>
      context.l10n.authErrorTooManyAttempts,
    'Could not connect to server' => context.l10n.authConnectServerError,
    'Server took too long to respond' => context.l10n.authErrorServerTimeout,
    'Connection failed. Please check the server URL.' =>
      context.l10n.authErrorCheckServerUrl,
    'Passkey authentication failed. Try signing in with your password.' =>
      context.l10n.authErrorPasskeyLogin,
    'Setup has already been completed' => context.l10n.authErrorSetupCompleted,
    'Setup failed. Please try again.' => context.l10n.authErrorSetupFailed,
    'Connection failed. The link may be invalid or expired.' =>
      context.l10n.authErrorConnectionLink,
    'Not authenticated' => context.l10n.authErrorNotAuthenticated,
    'Single sign-on is available on web, iOS and Android.' =>
      context.l10n.authErrorSsoPlatforms,
    'Single sign-on is not configured.' =>
      context.l10n.authErrorSsoUnconfigured,
    'The server returned an unexpected sign-in address.' =>
      context.l10n.authErrorUnexpectedSignInUrl,
    'Could not open the sign-in browser. Please try again.' =>
      context.l10n.authErrorOpenSignInBrowser,
    'This sign-in link is incomplete.' =>
      context.l10n.authErrorIncompleteSignInLink,
    'This link belongs to a different sign-in attempt.' =>
      context.l10n.authErrorDifferentSignIn,
    'Sign-in expired. Please start again.' =>
      context.l10n.authErrorSignInExpired,
    'Plex sign-in is still completing. Please wait.' =>
      context.l10n.authErrorPlexCompleting,
    'Invalid sign-in purpose.' => context.l10n.authErrorInvalidPurpose,
    'Finish or cancel the waiting Plex sign-in first.' =>
      context.l10n.authErrorPlexPending,
    'Plex sign-in is not available on this server.' =>
      context.l10n.authErrorPlexUnavailable,
    'The server returned an unexpected Plex address.' =>
      context.l10n.authErrorUnexpectedPlexUrl,
    'Plex sign-in cancelled.' => context.l10n.authErrorPlexCancelled,
    'Plex sign-in expired. Please try again.' =>
      context.l10n.authErrorPlexExpired,
    'Sign in to this server before linking Plex.' =>
      context.l10n.authErrorLinkPlexSignIn,
    'Passkeys are not supported on this platform' =>
      context.l10n.authErrorPasskeyPlatform,
    'WebAuthn not available' => context.l10n.authErrorWebauthnUnavailable,
    'User cancelled passkey registration' =>
      context.l10n.authErrorPasskeyRegistrationCancelled,
    'User cancelled passkey login' =>
      context.l10n.authErrorPasskeyUserLoginCancelled,
    'Passkey sign-in was cancelled.' =>
      context.l10n.authErrorPasskeyLoginCancelled,
    'Passkey creation was cancelled.' =>
      context.l10n.authErrorPasskeyCreationCancelled,
    'This server is not associated with this app for native passkeys.' =>
      context.l10n.authErrorPasskeyAssociation,
    'This device does not support passkeys.' =>
      context.l10n.authErrorPasskeyDevice,
    'Sign in to a Google account before creating Android passkeys.' =>
      context.l10n.authErrorGoogleSignIn,
    'The Android passkey sync account is not available. Try again after restarting the device.' =>
      context.l10n.authErrorPasskeySync,
    'No passkey provider is available. Enable a credential provider such as Bitwarden or Google Password Manager.' =>
      context.l10n.authErrorPasskeyProvider,
    'No passkey was found for this server.' =>
      context.l10n.authErrorPasskeyMissing,
    'A matching passkey is already registered on this device.' =>
      context.l10n.authErrorPasskeyDuplicate,
    'The passkey prompt timed out.' => context.l10n.authErrorPasskeyTimeout,
    'Passkey authentication failed.' =>
      context.l10n.authErrorPasskeyAuthentication,
    'Paste a connection link or enter your Cantinarr server address.' =>
      context.l10n.authErrorPasteConnection,
    'Check the connection link or server address.' =>
      context.l10n.authErrorCheckConnection,
    'This connection link is incomplete or invalid. Ask your server admin for a new link.' =>
      context.l10n.authErrorIncompleteConnection,
    'Enter your Cantinarr server address, such as http://192.168.1.10:8585.' =>
      context.l10n.authErrorEnterAddress,
    _ => message,
  };
}
