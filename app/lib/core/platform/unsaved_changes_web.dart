import 'dart:js_interop';

import 'package:web/web.dart' as web;

/// Browsers supply their own wording and require prior user interaction.
void Function() registerUnsavedChangesWarning(bool Function() hasChanges) {
  final listener = ((web.BeforeUnloadEvent event) {
    if (!hasChanges()) return;
    event.preventDefault();
    event.returnValue = 'unsaved';
  }).toJS;
  web.window.addEventListener('beforeunload', listener);
  return () => web.window.removeEventListener('beforeunload', listener);
}
