import 'package:flutter/material.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter/semantics.dart';
import 'app.dart';
import 'core/automation/web_semantics.dart';
import 'core/network/app_image_cache.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  if (!kIsWeb) {
    try {
      await purgeLegacyImageCache();
    } catch (_) {
      // Try again on the next launch; protected art no longer reads this cache.
    }
  }
  final e2eWebSemantics = shouldEnableCurrentWebSemantics();
  if (e2eWebSemantics) {
    enableE2EWebSemantics();
  }
  runApp(const ProviderScope(child: CantinarrApp()));
  if (e2eWebSemantics) {
    // Flutter web keeps its semantic DOM off by default for performance. The
    // explicit E2E opt-in gives accessibility tooling and black-box tests a
    // stable tree without imposing that cost on normal production sessions.
    SemanticsBinding.instance.ensureSemantics();
  }
}
