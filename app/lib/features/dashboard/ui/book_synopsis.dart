import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';

/// A synopsis can be visually collapsed, already abbreviated by its source,
/// or still loading. Expanding is a reader choice, independent of the fetch.
class BookSynopsis extends StatefulWidget {
  final String text;
  final bool loading;
  final bool failed;
  final VoidCallback onRetry;
  const BookSynopsis(
      {super.key,
      required this.text,
      required this.loading,
      required this.failed,
      required this.onRetry});

  @override
  State<BookSynopsis> createState() => _BookSynopsisState();
}

class _BookSynopsisState extends State<BookSynopsis> {
  bool _expanded = false;
  bool _askedForMore = false;

  @override
  Widget build(BuildContext context) {
    final text = widget.text;
    if (text.isEmpty && !widget.loading && !widget.failed) {
      return const SizedBox.shrink();
    }
    final style = Theme.of(context).textTheme.bodyMedium?.copyWith(
          color: AppTheme.textPrimary,
        );
    return Padding(
      padding: const EdgeInsets.only(top: 24),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Text('About this book', style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 8),
        LayoutBuilder(builder: (context, constraints) {
          final painter = TextPainter(
            text: TextSpan(text: text, style: style),
            maxLines: 4,
            textDirection: Directionality.of(context),
            textScaler: MediaQuery.textScalerOf(context),
            locale: Localizations.maybeLocaleOf(context),
          )..layout(maxWidth: constraints.maxWidth);
          final overflows = painter.didExceedMaxLines;
          painter.dispose();
          final abbreviated = RegExp(r'(\.\.\.|…)\s*$').hasMatch(text);
          final canExpand =
              overflows || (abbreviated && (widget.loading || widget.failed));
          return Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (text.isNotEmpty)
                  Text(
                    text,
                    key: const ValueKey('book-synopsis-text'),
                    maxLines: _expanded ? null : 4,
                    overflow: _expanded
                        ? TextOverflow.visible
                        : TextOverflow.ellipsis,
                    style: style,
                  ),
                if (canExpand)
                  TextButton(
                    onPressed: () => setState(() {
                      _expanded = !_expanded;
                      _askedForMore = true;
                    }),
                    child: Text(_expanded ? 'Read less' : 'Read more'),
                  ),
                if (widget.loading && (_expanded || text.isEmpty))
                  const Padding(
                    padding: EdgeInsets.only(top: 8),
                    child: Row(children: [
                      SizedBox(
                          width: 14,
                          height: 14,
                          child: CircularProgressIndicator(strokeWidth: 2)),
                      SizedBox(width: 8),
                      Flexible(child: Text('Loading book details…')),
                    ]),
                  ),
                if (widget.failed)
                  Wrap(
                    crossAxisAlignment: WrapCrossAlignment.center,
                    children: [
                      const Text('Couldn’t load more details'),
                      TextButton(
                          onPressed: widget.onRetry,
                          child: const Text('Retry')),
                    ],
                  ),
                if (_askedForMore &&
                    !widget.loading &&
                    !widget.failed &&
                    abbreviated)
                  const Padding(
                      padding: EdgeInsets.only(top: 8),
                      child: Text(
                          'No longer description is available from the book source.')),
              ]);
        }),
      ]),
    );
  }
}
