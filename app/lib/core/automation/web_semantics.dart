import 'package:flutter/foundation.dart';

const bool e2eWebSemanticsBuildEnabled = bool.fromEnvironment(
  'CANTINARR_E2E_WEB_SEMANTICS',
);

bool shouldEnableCurrentWebSemantics() => kIsWeb && e2eWebSemanticsBuildEnabled;

bool _e2eWebSemanticsEnabled = false;

bool get e2eWebSemanticsEnabled => _e2eWebSemanticsEnabled;

void enableE2EWebSemantics() {
  if (kIsWeb) _e2eWebSemanticsEnabled = true;
}
