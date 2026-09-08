import '../../../core/utils/plain_text_metadata.dart';

// Goodreads sometimes prepends a link to a different cover. It describes the
// listing, not the story, and plain-text rendering leaves a dangling "here".
// Match only the standalone notice so ordinary synopsis prose is preserved.
final _alternateCoverNotice = RegExp(
  r'^(?:an? )?alternat(?:e|ive) cover(?: edition)?'
  r'(?: (?:for|of) this (?:ASIN|ISBN))? can be found here[.!]?$',
  caseSensitive: false,
);

String bookDescriptionText(String? value) => metadataPlainText(value)
    .split('\n\n')
    .where((paragraph) => !_alternateCoverNotice.hasMatch(paragraph))
    .join('\n\n');
