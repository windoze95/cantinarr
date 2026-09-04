import 'dart:io';

import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// The switch thumb must never be painted the same colour as the active
/// track.
///
/// `SwitchThemeData` already resolves a selected switch to an [AppTheme.accent]
/// track with an [AppTheme.onAccent] thumb. Sixteen call sites across eleven
/// files had each passed `activeThumbColor: AppTheme.accent`, repainting the
/// thumb the *same amber as the track underneath it* — so every "on" switch in
/// the app rendered as a solid amber pill with no thumb visible at all, and
/// read as a filled blob rather than a control with a position. The overrides
/// are gone; these tests keep them gone, because the failure is invisible to
/// the type system and spread by copy-paste.
void main() {
  test('the selected switch thumb contrasts with its track', () {
    final switchTheme = AppTheme.dark.switchTheme;
    const selected = <WidgetState>{WidgetState.selected};

    final thumb = switchTheme.thumbColor?.resolve(selected);
    final track = switchTheme.trackColor?.resolve(selected);

    expect(thumb, AppTheme.onAccent);
    expect(track, AppTheme.accent);
    // The assertion that actually describes the defect: whatever the two
    // colours are, a thumb the same colour as its track is not visible.
    expect(thumb, isNot(track));
  });

  test('no call site repaints the active thumb with the track colour', () {
    final offenders = <String>[];
    for (final entity in Directory('lib').listSync(recursive: true)) {
      if (entity is! File || !entity.path.endsWith('.dart')) continue;
      final lines = entity.readAsLinesSync();
      for (var i = 0; i < lines.length; i++) {
        if (lines[i].contains('activeThumbColor: AppTheme.accent')) {
          offenders.add('${entity.path}:${i + 1}');
        }
      }
    }
    expect(
      offenders,
      isEmpty,
      reason: 'Setting activeThumbColor to AppTheme.accent paints the thumb '
          'the same amber as the active track, so the switch renders as a '
          'solid pill. Drop the override and let SwitchThemeData resolve it.',
    );
  });
}
