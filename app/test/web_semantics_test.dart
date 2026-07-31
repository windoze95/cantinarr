import 'package:cantinarr/core/automation/web_semantics.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('E2E web semantics require both a web target and the build define', () {
    expect(
      shouldEnableCurrentWebSemantics(),
      kIsWeb && e2eWebSemanticsBuildEnabled,
    );
  });
}
