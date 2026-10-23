import 'package:flutter/widgets.dart';
import 'package:cantinarr/l10n/l10n.dart';

import '../push_service.dart';

/// A presentation-only version of the push test result. Provider codes and
/// registration results retain their original values.
String localizedDescribePushTest(BuildContext context, PushTestResult result,
    {String? username}) {
  final l10n = context.l10n;
  if (l10n.localeName == 'en') {
    return describePushTest(result, username: username);
  }
  if (result.tokens == 0) {
    return username == null
        ? l10n.notificationNoDeviceSelf
        : l10n.notificationNoDeviceUser(username);
  }
  String devices(int count) => count == 1
      ? l10n.settings1Device
      : l10n.settingsMessageValue1Devices(count.toString());
  if (result.sent > 0 && result.failed == 0) {
    return username == null
        ? l10n.notificationTestSent(devices(result.sent))
        : l10n.notificationTestSentUser(username, devices(result.sent));
  }
  if (result.sent == 0 && result.failed == 0) {
    return l10n
        .notificationGatewayNoToken(username ?? l10n.notificationThisAccount);
  }
  final error = result.firstError;
  final hint = switch (error) {
    null || '' => '',
    final String value when value.contains('UNREGISTERED') =>
      l10n.notificationGoogleTokenInvalid,
    final String value when value.contains('SENDER_ID_MISMATCH') =>
      l10n.notificationFirebaseProjectMismatch,
    final String value when value.contains('INVALID_ARGUMENT') =>
      l10n.notificationGoogleRejected,
    final String value when value.contains('provider_not_enabled') =>
      l10n.notificationProviderNotEnabled,
    final String value when value.contains('BadDeviceToken') =>
      l10n.notificationAppleTokenInvalid,
    final String value when value.contains('Unregistered') =>
      l10n.notificationAppleTokenExpired,
    _ => ' ($error)',
  };
  if (result.sent > 0) {
    return l10n.notificationPartialTest(
        result.sent.toString(), result.failed.toString(), hint);
  }
  return username == null
      ? l10n.notificationTestFailed(devices(result.failed), hint)
      : l10n.notificationTestFailedUser(username, devices(result.failed), hint);
}
