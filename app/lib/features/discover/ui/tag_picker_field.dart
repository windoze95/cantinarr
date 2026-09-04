import 'dart:async';

import 'package:flutter/material.dart';

import '../../../core/config/app_config.dart';
import '../../../core/theme/app_theme.dart';
import '../data/tmdb_models.dart';

/// A type-to-add field for keywords and studios: the chosen values as
/// removable chips, a text field, and TMDB's suggestions for what was typed
/// listed inline under the field (inline rather than an overlay so the list
/// scrolls with the sheet instead of clipping against it).
class TagPickerField extends StatefulWidget {
  const TagPickerField({
    super.key,
    required this.kind,
    required this.values,
    required this.onChanged,
    required this.search,
    required this.hint,
    required this.failureMessage,
    this.maxSuggestions = 8,
  });

  /// Names an unnamed value: "keyword 123", "studio 456".
  final String kind;
  final List<TaggedId> values;
  final ValueChanged<List<TaggedId>> onChanged;
  final Future<List<TaggedId>> Function(String query) search;
  final String hint;

  /// Shown under the field when a lookup fails, so an empty suggestion list
  /// never passes for "nothing matched".
  final String failureMessage;
  final int maxSuggestions;

  @override
  State<TagPickerField> createState() => _TagPickerFieldState();
}

class _TagPickerFieldState extends State<TagPickerField> {
  final TextEditingController _controller = TextEditingController();
  Timer? _debounce;

  /// Bumped on every edit so a slow answer to an earlier query never
  /// replaces the suggestions for what is typed now.
  int _generation = 0;
  List<TaggedId> _suggestions = const [];
  bool _searching = false;
  bool _failed = false;

  @override
  void dispose() {
    _debounce?.cancel();
    _controller.dispose();
    super.dispose();
  }

  void _onTextChanged(String text) {
    _debounce?.cancel();
    final generation = ++_generation;
    final query = text.trim();
    if (query.isEmpty) {
      setState(() {
        _suggestions = const [];
        _searching = false;
        _failed = false;
      });
      return;
    }
    setState(() {
      _searching = true;
      _failed = false;
    });
    _debounce = Timer(AppConfig.searchDebounce, () => _run(query, generation));
  }

  Future<void> _run(String query, int generation) async {
    try {
      final results = await widget.search(query);
      if (!mounted || generation != _generation) return;
      final chosen = {for (final value in widget.values) value.id};
      setState(() {
        _searching = false;
        _suggestions = [
          for (final result in results)
            if (!chosen.contains(result.id)) result,
        ].take(widget.maxSuggestions).toList(growable: false);
      });
    } catch (_) {
      if (!mounted || generation != _generation) return;
      setState(() {
        _searching = false;
        _failed = true;
        _suggestions = const [];
      });
    }
  }

  void _pick(TaggedId tag) {
    _debounce?.cancel();
    _generation++;
    _controller.clear();
    setState(() {
      _suggestions = const [];
      _searching = false;
      _failed = false;
    });
    widget.onChanged([...widget.values, tag]);
  }

  String _label(TaggedId tag) => tag.name ?? '${widget.kind} ${tag.id}';

  @override
  Widget build(BuildContext context) {
    final textTheme = Theme.of(context).textTheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (widget.values.isNotEmpty) ...[
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final value in widget.values)
                InputChip(
                  label: Text(_label(value)),
                  labelStyle: const TextStyle(color: AppTheme.textPrimary),
                  backgroundColor: AppTheme.surfaceVariant,
                  deleteIcon: const Icon(Icons.close, size: 16),
                  deleteIconColor: AppTheme.textSecondary,
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(20),
                    side: const BorderSide(color: AppTheme.border),
                  ),
                  materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  visualDensity: VisualDensity.compact,
                  onDeleted: () => widget.onChanged([
                    for (final other in widget.values)
                      if (other != value) other,
                  ]),
                ),
            ],
          ),
          const SizedBox(height: 8),
        ],
        TextField(
          controller: _controller,
          onChanged: _onTextChanged,
          textInputAction: TextInputAction.done,
          decoration: InputDecoration(
            hintText: widget.hint,
            isDense: true,
            suffixIcon: _searching
                ? const Padding(
                    padding: EdgeInsets.all(12),
                    child: SizedBox(
                      width: 16,
                      height: 16,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    ),
                  )
                : null,
          ),
        ),
        if (_failed)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Text(
              widget.failureMessage,
              style: textTheme.bodySmall?.copyWith(color: AppTheme.textMuted),
            ),
          ),
        for (final suggestion in _suggestions)
          ListTile(
            dense: true,
            contentPadding: EdgeInsets.zero,
            visualDensity: VisualDensity.compact,
            leading: const Icon(Icons.add, size: 18),
            title: Text(_label(suggestion)),
            onTap: () => _pick(suggestion),
          ),
      ],
    );
  }
}
