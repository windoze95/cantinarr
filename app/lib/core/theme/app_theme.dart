import 'package:flutter/material.dart';

/// Cantinarr's dark-first, warm cinematic design system.
///
/// The legacy colour names remain part of this class because feature widgets
/// use them directly. New shared UI should prefer [Theme.of] and the semantic
/// tokens below so interaction states and component contrast stay consistent.
abstract final class AppTheme {
  // Core palette.
  static const Color background = Color(0xFF0C0805);
  static const Color surface = Color(0xFF15100C);
  static const Color surfaceVariant = Color(0xFF201710);
  static const Color surfaceRaised = Color(0xFF2A1E14);
  static const Color surfaceElevated = Color(0xFF35261A);

  static const Color accent = Color(0xFFF2AC2D);
  static const Color onAccent = Color(0xFF201505);
  static const Color accentContainer = Color(0xFF573916);
  static const Color signal = Color(0xFFF47A2E);
  static const Color signalContainer = Color(0xFF4A2115);

  static const Color textPrimary = Color(0xFFF6F0E8);
  static const Color textSecondary = Color(0xFFB7A99D);
  static const Color textMuted = Color(0xFFA09286);
  static const Color border = Color(0xFF3B2B20);
  static const Color borderStrong = Color(0xFF5A4130);

  // Semantic colours.
  static const Color success = Color(0xFF72CC91);
  static const Color warning = Color(0xFFF4C66A);
  static const Color info = Color(0xFFD98A58);
  static const Color danger = Color(0xFFF17878);

  // Compatibility names used throughout existing feature UI.
  static const Color available = success;
  static const Color requested = warning;
  static const Color downloading = info;
  static const Color unavailable = textMuted;
  static const Color error = danger;

  // Spacing, shape, and motion tokens for shared components.
  static const double spaceXs = 4;
  static const double spaceSm = 8;
  static const double spaceMd = 12;
  static const double spaceLg = 16;
  static const double spaceXl = 24;
  static const double space2xl = 32;
  static const double space3xl = 48;

  static const double radiusSm = 8;
  static const double radiusMd = 12;
  static const double radiusLg = 16;
  static const double radiusXl = 24;
  static const double radiusPill = 999;

  // Verbose aliases keep call sites readable without splitting the token set.
  static const double radiusSmall = radiusSm;
  static const double radiusMedium = radiusMd;
  static const double radiusLarge = radiusLg;
  static const double radiusXLarge = radiusXl;

  static const Duration motionFast = Duration(milliseconds: 120);
  static const Duration motionMedium = Duration(milliseconds: 220);
  static const Duration motionSlow = Duration(milliseconds: 360);

  static const ColorScheme _darkColorScheme = ColorScheme.dark(
    primary: accent,
    onPrimary: onAccent,
    primaryContainer: accentContainer,
    onPrimaryContainer: Color(0xFFFFE0A3),
    secondary: signal,
    onSecondary: Color(0xFF2B0D03),
    secondaryContainer: signalContainer,
    onSecondaryContainer: Color(0xFFFFD8C8),
    tertiary: success,
    onTertiary: Color(0xFF041710),
    tertiaryContainer: Color(0xFF143D2C),
    onTertiaryContainer: Color(0xFFC2F5DB),
    error: danger,
    onError: Color(0xFF210609),
    errorContainer: Color(0xFF4A1C25),
    onErrorContainer: Color(0xFFFFD9DE),
    surface: surface,
    onSurface: textPrimary,
    surfaceDim: background,
    surfaceBright: surfaceElevated,
    surfaceContainerLowest: background,
    surfaceContainerLow: surface,
    surfaceContainer: surfaceVariant,
    surfaceContainerHigh: surfaceRaised,
    surfaceContainerHighest: surfaceElevated,
    onSurfaceVariant: textSecondary,
    outline: borderStrong,
    outlineVariant: border,
    shadow: Color(0xFF000000),
    scrim: Color(0xD9000000),
    inverseSurface: textPrimary,
    onInverseSurface: background,
    inversePrimary: Color(0xFF7C4F08),
    surfaceTint: Colors.transparent,
  );

  static const TextTheme _textTheme = TextTheme(
    displayLarge: TextStyle(
      color: textPrimary,
      fontSize: 48,
      height: 1.05,
      fontWeight: FontWeight.w700,
      letterSpacing: -1.4,
    ),
    displayMedium: TextStyle(
      color: textPrimary,
      fontSize: 40,
      height: 1.08,
      fontWeight: FontWeight.w700,
      letterSpacing: -1.1,
    ),
    displaySmall: TextStyle(
      color: textPrimary,
      fontSize: 34,
      height: 1.1,
      fontWeight: FontWeight.w700,
      letterSpacing: -0.8,
    ),
    headlineLarge: TextStyle(
      color: textPrimary,
      fontSize: 30,
      height: 1.15,
      fontWeight: FontWeight.w700,
      letterSpacing: -0.6,
    ),
    headlineMedium: TextStyle(
      color: textPrimary,
      fontSize: 26,
      height: 1.18,
      fontWeight: FontWeight.w700,
      letterSpacing: -0.4,
    ),
    headlineSmall: TextStyle(
      color: textPrimary,
      fontSize: 22,
      height: 1.2,
      fontWeight: FontWeight.w700,
      letterSpacing: -0.2,
    ),
    titleLarge: TextStyle(
      color: textPrimary,
      fontSize: 20,
      height: 1.25,
      fontWeight: FontWeight.w600,
      letterSpacing: -0.2,
    ),
    titleMedium: TextStyle(
      color: textPrimary,
      fontSize: 16,
      height: 1.3,
      fontWeight: FontWeight.w600,
    ),
    titleSmall: TextStyle(
      color: textPrimary,
      fontSize: 14,
      height: 1.3,
      fontWeight: FontWeight.w600,
      letterSpacing: 0.1,
    ),
    bodyLarge: TextStyle(
      color: textPrimary,
      fontSize: 16,
      height: 1.5,
      fontWeight: FontWeight.w400,
    ),
    bodyMedium: TextStyle(
      color: textSecondary,
      fontSize: 14,
      height: 1.45,
      fontWeight: FontWeight.w400,
    ),
    bodySmall: TextStyle(
      color: textMuted,
      fontSize: 12,
      height: 1.4,
      fontWeight: FontWeight.w400,
    ),
    labelLarge: TextStyle(
      color: textPrimary,
      fontSize: 14,
      height: 1.2,
      fontWeight: FontWeight.w600,
      letterSpacing: 0.15,
    ),
    labelMedium: TextStyle(
      color: textSecondary,
      fontSize: 12,
      height: 1.2,
      fontWeight: FontWeight.w600,
      letterSpacing: 0.25,
    ),
    labelSmall: TextStyle(
      color: textMuted,
      fontSize: 11,
      height: 1.2,
      fontWeight: FontWeight.w600,
      letterSpacing: 0.45,
    ),
  );

  static ThemeData get dark {
    final shape = RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(radiusMd),
    );
    final outlinedShape = RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(radiusLg),
      side: const BorderSide(color: border),
    );

    return ThemeData(
      brightness: Brightness.dark,
      useMaterial3: true,
      colorScheme: _darkColorScheme,
      textTheme: _textTheme,
      primaryTextTheme: _textTheme,
      scaffoldBackgroundColor: Colors.transparent,
      canvasColor: background,
      cardColor: surfaceVariant,
      dividerColor: border,
      disabledColor: textMuted.withValues(alpha: 0.45),
      focusColor: accent.withValues(alpha: 0.24),
      hoverColor: textPrimary.withValues(alpha: 0.05),
      highlightColor: accent.withValues(alpha: 0.08),
      splashColor: accent.withValues(alpha: 0.12),
      shadowColor: Colors.black.withValues(alpha: 0.55),
      iconTheme: const IconThemeData(color: textSecondary, size: 22),
      primaryIconTheme: const IconThemeData(color: onAccent, size: 22),
      visualDensity: VisualDensity.standard,
      materialTapTargetSize: MaterialTapTargetSize.padded,
      scrollbarTheme: ScrollbarThemeData(
        radius: const Radius.circular(radiusPill),
        thickness: const WidgetStatePropertyAll(5),
        thumbColor: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.dragged)) return borderStrong;
          if (states.contains(WidgetState.hovered)) {
            return textMuted.withValues(alpha: 0.75);
          }
          return borderStrong.withValues(alpha: 0.7);
        }),
        trackColor: const WidgetStatePropertyAll(Colors.transparent),
      ),
      appBarTheme: const AppBarTheme(
        backgroundColor: Colors.transparent,
        foregroundColor: textPrimary,
        surfaceTintColor: Colors.transparent,
        shadowColor: Colors.transparent,
        elevation: 0,
        scrolledUnderElevation: 0,
        centerTitle: false,
        toolbarHeight: 64,
        titleTextStyle: TextStyle(
          color: textPrimary,
          fontSize: 19,
          height: 1.2,
          fontWeight: FontWeight.w600,
          letterSpacing: -0.2,
        ),
        iconTheme: IconThemeData(color: textSecondary, size: 22),
        actionsIconTheme: IconThemeData(color: textSecondary, size: 22),
      ),
      cardTheme: CardThemeData(
        color: surfaceVariant,
        surfaceTintColor: Colors.transparent,
        shadowColor: Colors.black.withValues(alpha: 0.45),
        elevation: 0,
        clipBehavior: Clip.antiAlias,
        shape: outlinedShape,
      ),
      dividerTheme: const DividerThemeData(
        color: border,
        thickness: 1,
        space: 1,
      ),
      badgeTheme: const BadgeThemeData(
        backgroundColor: accent,
        textColor: onAccent,
        smallSize: 8,
        largeSize: 20,
        padding: EdgeInsets.symmetric(horizontal: 6),
        textStyle: TextStyle(fontSize: 11, fontWeight: FontWeight.w700),
      ),
      drawerTheme: const DrawerThemeData(
        backgroundColor: surface,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.only(
            topRight: Radius.circular(radiusXl),
            bottomRight: Radius.circular(radiusXl),
          ),
        ),
      ),
      navigationBarTheme: NavigationBarThemeData(
        height: 72,
        backgroundColor: surface,
        surfaceTintColor: Colors.transparent,
        indicatorColor: accent.withValues(alpha: 0.16),
        elevation: 0,
        labelBehavior: NavigationDestinationLabelBehavior.alwaysShow,
        iconTheme: WidgetStateProperty.resolveWith((states) {
          return IconThemeData(
            color: states.contains(WidgetState.selected) ? accent : textMuted,
            size: states.contains(WidgetState.selected) ? 24 : 22,
          );
        }),
        labelTextStyle: WidgetStateProperty.resolveWith((states) {
          return _textTheme.labelMedium!.copyWith(
            color: states.contains(WidgetState.selected) ? accent : textMuted,
            fontWeight: states.contains(WidgetState.selected)
                ? FontWeight.w700
                : FontWeight.w500,
          );
        }),
        overlayColor: _interactionOverlay(accent),
      ),
      bottomNavigationBarTheme: const BottomNavigationBarThemeData(
        backgroundColor: surface,
        selectedItemColor: accent,
        unselectedItemColor: textMuted,
        elevation: 0,
        type: BottomNavigationBarType.fixed,
        selectedLabelStyle: TextStyle(
          fontSize: 12,
          fontWeight: FontWeight.w700,
        ),
        unselectedLabelStyle: TextStyle(
          fontSize: 12,
          fontWeight: FontWeight.w500,
        ),
      ),
      navigationRailTheme: NavigationRailThemeData(
        backgroundColor: surface,
        indicatorColor: accent.withValues(alpha: 0.16),
        elevation: 0,
        useIndicator: true,
        selectedIconTheme: const IconThemeData(color: accent, size: 23),
        unselectedIconTheme: const IconThemeData(color: textMuted, size: 22),
        selectedLabelTextStyle: _textTheme.labelMedium!.copyWith(color: accent),
        unselectedLabelTextStyle:
            _textTheme.labelMedium!.copyWith(color: textMuted),
      ),
      listTileTheme: ListTileThemeData(
        iconColor: textSecondary,
        textColor: textPrimary,
        selectedColor: accent,
        selectedTileColor: accent.withValues(alpha: 0.09),
        titleTextStyle: _textTheme.titleSmall,
        subtitleTextStyle: _textTheme.bodySmall,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: spaceLg,
          vertical: spaceXs,
        ),
        minLeadingWidth: 32,
        horizontalTitleGap: 12,
        shape: shape,
      ),
      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: surfaceVariant,
        hoverColor: surfaceRaised,
        labelStyle: _textTheme.bodyMedium,
        floatingLabelStyle: _textTheme.labelMedium!.copyWith(color: accent),
        hintStyle: _textTheme.bodyMedium!.copyWith(color: textMuted),
        helperStyle: _textTheme.bodySmall,
        errorStyle: _textTheme.bodySmall!.copyWith(color: danger),
        prefixIconColor: textMuted,
        suffixIconColor: textMuted,
        contentPadding: const EdgeInsets.symmetric(
          horizontal: spaceLg,
          vertical: 15,
        ),
        border: _inputBorder(border),
        enabledBorder: _inputBorder(border),
        focusedBorder: _inputBorder(accent, width: 1.5),
        errorBorder: _inputBorder(danger),
        focusedErrorBorder: _inputBorder(danger, width: 1.5),
        disabledBorder: _inputBorder(border.withValues(alpha: 0.5)),
      ),
      elevatedButtonTheme: ElevatedButtonThemeData(
        style: _filledButtonStyle(
          backgroundColor: accent,
          foregroundColor: onAccent,
        ),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: _filledButtonStyle(
          backgroundColor: accent,
          foregroundColor: onAccent,
        ),
      ),
      outlinedButtonTheme: OutlinedButtonThemeData(
        style: ButtonStyle(
          foregroundColor: _foreground(textPrimary),
          backgroundColor: const WidgetStatePropertyAll(Colors.transparent),
          overlayColor: _interactionOverlay(accent),
          minimumSize: const WidgetStatePropertyAll(Size(48, 48)),
          padding: const WidgetStatePropertyAll(
            EdgeInsets.symmetric(horizontal: spaceLg, vertical: spaceMd),
          ),
          textStyle: WidgetStatePropertyAll(_textTheme.labelLarge),
          shape: WidgetStatePropertyAll(shape),
          side: WidgetStateProperty.resolveWith((states) {
            if (states.contains(WidgetState.disabled)) {
              return BorderSide(color: border.withValues(alpha: 0.5));
            }
            if (states.contains(WidgetState.focused)) {
              return const BorderSide(color: accent, width: 1.5);
            }
            return const BorderSide(color: borderStrong);
          }),
        ),
      ),
      textButtonTheme: TextButtonThemeData(
        style: ButtonStyle(
          foregroundColor: _foreground(accent),
          overlayColor: _interactionOverlay(accent),
          minimumSize: const WidgetStatePropertyAll(Size(44, 44)),
          padding: const WidgetStatePropertyAll(
            EdgeInsets.symmetric(horizontal: spaceMd, vertical: spaceSm),
          ),
          textStyle: WidgetStatePropertyAll(_textTheme.labelLarge),
          shape: WidgetStatePropertyAll(shape),
        ),
      ),
      iconButtonTheme: IconButtonThemeData(
        style: ButtonStyle(
          foregroundColor: _foreground(textSecondary),
          backgroundColor: WidgetStateProperty.resolveWith((states) {
            if (states.contains(WidgetState.selected)) {
              return accent.withValues(alpha: 0.14);
            }
            return Colors.transparent;
          }),
          overlayColor: _interactionOverlay(accent),
          minimumSize: const WidgetStatePropertyAll(Size(44, 44)),
          iconSize: const WidgetStatePropertyAll(22),
          shape: WidgetStatePropertyAll(
            RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(radiusMd),
            ),
          ),
        ),
      ),
      floatingActionButtonTheme: const FloatingActionButtonThemeData(
        backgroundColor: accent,
        foregroundColor: onAccent,
        elevation: 0,
        focusElevation: 0,
        hoverElevation: 2,
        highlightElevation: 0,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.all(Radius.circular(radiusLg)),
        ),
      ),
      chipTheme: ChipThemeData(
        backgroundColor: surfaceVariant,
        selectedColor: accent.withValues(alpha: 0.16),
        disabledColor: surfaceVariant.withValues(alpha: 0.5),
        checkmarkColor: accent,
        labelStyle: _textTheme.labelMedium!,
        secondaryLabelStyle: _textTheme.labelMedium!.copyWith(color: accent),
        side: const BorderSide(color: border),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(radiusSm),
        ),
        padding: const EdgeInsets.symmetric(horizontal: spaceSm),
        labelPadding: const EdgeInsets.symmetric(horizontal: spaceXs),
      ),
      checkboxTheme: CheckboxThemeData(
        fillColor: _selectionFill(accent),
        checkColor: const WidgetStatePropertyAll(onAccent),
        side: const BorderSide(color: borderStrong, width: 1.5),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(5),
        ),
      ),
      radioTheme: RadioThemeData(
        fillColor: _selectionFill(accent),
        overlayColor: _interactionOverlay(accent),
      ),
      switchTheme: SwitchThemeData(
        thumbColor: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.disabled)) return textMuted;
          if (states.contains(WidgetState.selected)) return onAccent;
          return textSecondary;
        }),
        trackColor: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.disabled)) {
            return border.withValues(alpha: 0.45);
          }
          if (states.contains(WidgetState.selected)) return accent;
          return surfaceElevated;
        }),
        trackOutlineColor: WidgetStateProperty.resolveWith((states) {
          return states.contains(WidgetState.selected)
              ? Colors.transparent
              : borderStrong;
        }),
        overlayColor: _interactionOverlay(accent),
      ),
      sliderTheme: const SliderThemeData(
        activeTrackColor: accent,
        inactiveTrackColor: border,
        thumbColor: accent,
        overlayColor: Color(0x33F2AC2D),
        valueIndicatorColor: surfaceElevated,
        valueIndicatorTextStyle: TextStyle(
          color: textPrimary,
          fontWeight: FontWeight.w600,
        ),
      ),
      progressIndicatorTheme: const ProgressIndicatorThemeData(
        color: accent,
        linearTrackColor: border,
        circularTrackColor: border,
        linearMinHeight: 5,
      ),
      tabBarTheme: TabBarThemeData(
        labelColor: accent,
        unselectedLabelColor: textMuted,
        labelStyle: _textTheme.labelLarge,
        unselectedLabelStyle: _textTheme.labelLarge,
        dividerColor: Colors.transparent,
        indicatorSize: TabBarIndicatorSize.tab,
        indicator: BoxDecoration(
          color: accent.withValues(alpha: 0.14),
          borderRadius: BorderRadius.circular(radiusMd),
        ),
        overlayColor: _interactionOverlay(accent),
      ),
      segmentedButtonTheme: SegmentedButtonThemeData(
        style: ButtonStyle(
          foregroundColor: WidgetStateProperty.resolveWith((states) {
            if (states.contains(WidgetState.selected)) return accent;
            return textSecondary;
          }),
          backgroundColor: WidgetStateProperty.resolveWith((states) {
            if (states.contains(WidgetState.selected)) {
              return accent.withValues(alpha: 0.14);
            }
            return Colors.transparent;
          }),
          overlayColor: _interactionOverlay(accent),
          textStyle: WidgetStatePropertyAll(_textTheme.labelMedium),
          side: const WidgetStatePropertyAll(BorderSide(color: border)),
          shape: WidgetStatePropertyAll(shape),
          padding: const WidgetStatePropertyAll(
            EdgeInsets.symmetric(horizontal: spaceMd, vertical: spaceSm),
          ),
        ),
      ),
      bottomSheetTheme: const BottomSheetThemeData(
        backgroundColor: surfaceRaised,
        modalBackgroundColor: surfaceRaised,
        surfaceTintColor: Colors.transparent,
        elevation: 0,
        modalElevation: 0,
        showDragHandle: true,
        dragHandleColor: borderStrong,
        dragHandleSize: Size(40, 4),
        constraints: BoxConstraints(maxWidth: 640),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.vertical(top: Radius.circular(radiusXl)),
          side: BorderSide(color: border),
        ),
      ),
      dialogTheme: DialogThemeData(
        backgroundColor: surfaceRaised,
        surfaceTintColor: Colors.transparent,
        shadowColor: Colors.black.withValues(alpha: 0.7),
        elevation: 18,
        shape: outlinedShape,
        titleTextStyle: _textTheme.titleLarge,
        contentTextStyle: _textTheme.bodyMedium,
        actionsPadding: const EdgeInsets.fromLTRB(
          spaceLg,
          spaceSm,
          spaceLg,
          spaceLg,
        ),
      ),
      snackBarTheme: SnackBarThemeData(
        backgroundColor: surfaceElevated,
        actionTextColor: accent,
        disabledActionTextColor: textMuted,
        contentTextStyle: _textTheme.bodyMedium!.copyWith(color: textPrimary),
        behavior: SnackBarBehavior.floating,
        elevation: 12,
        insetPadding: const EdgeInsets.all(spaceLg),
        shape: outlinedShape,
      ),
      popupMenuTheme: PopupMenuThemeData(
        color: surfaceRaised,
        surfaceTintColor: Colors.transparent,
        elevation: 12,
        shadowColor: Colors.black.withValues(alpha: 0.65),
        shape: outlinedShape,
        textStyle: _textTheme.bodyMedium,
        labelTextStyle: WidgetStatePropertyAll(_textTheme.bodyMedium),
      ),
      menuTheme: MenuThemeData(
        style: MenuStyle(
          backgroundColor: const WidgetStatePropertyAll(surfaceRaised),
          surfaceTintColor: const WidgetStatePropertyAll(Colors.transparent),
          shadowColor: WidgetStatePropertyAll(
            Colors.black.withValues(alpha: 0.65),
          ),
          elevation: const WidgetStatePropertyAll(12),
          side: const WidgetStatePropertyAll(BorderSide(color: border)),
          shape: WidgetStatePropertyAll(shape),
          padding: const WidgetStatePropertyAll(EdgeInsets.all(spaceXs)),
        ),
      ),
      dropdownMenuTheme: DropdownMenuThemeData(
        textStyle: _textTheme.bodyMedium,
        inputDecorationTheme: InputDecorationTheme(
          filled: true,
          fillColor: surfaceVariant,
          border: _inputBorder(border),
          enabledBorder: _inputBorder(border),
          focusedBorder: _inputBorder(accent, width: 1.5),
        ),
        menuStyle: MenuStyle(
          backgroundColor: const WidgetStatePropertyAll(surfaceRaised),
          surfaceTintColor: const WidgetStatePropertyAll(Colors.transparent),
          side: const WidgetStatePropertyAll(BorderSide(color: border)),
          shape: WidgetStatePropertyAll(shape),
        ),
      ),
      searchBarTheme: SearchBarThemeData(
        backgroundColor: const WidgetStatePropertyAll(surfaceVariant),
        surfaceTintColor: const WidgetStatePropertyAll(Colors.transparent),
        shadowColor: const WidgetStatePropertyAll(Colors.transparent),
        elevation: const WidgetStatePropertyAll(0),
        side: WidgetStateProperty.resolveWith((states) {
          return BorderSide(
            color: states.contains(WidgetState.focused) ? accent : border,
            width: states.contains(WidgetState.focused) ? 1.5 : 1,
          );
        }),
        shape: WidgetStatePropertyAll(
          RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(radiusLg),
          ),
        ),
        textStyle: WidgetStatePropertyAll(_textTheme.bodyLarge),
        hintStyle: WidgetStatePropertyAll(
          _textTheme.bodyLarge!.copyWith(color: textMuted),
        ),
        padding: const WidgetStatePropertyAll(
          EdgeInsets.symmetric(horizontal: spaceLg),
        ),
      ),
      dataTableTheme: DataTableThemeData(
        headingRowColor: const WidgetStatePropertyAll(surfaceVariant),
        dataRowColor: const WidgetStatePropertyAll(Colors.transparent),
        headingTextStyle: _textTheme.labelMedium!.copyWith(color: textPrimary),
        dataTextStyle: _textTheme.bodyMedium,
        dividerThickness: 1,
        headingRowHeight: 48,
        dataRowMinHeight: 52,
        dataRowMaxHeight: 68,
        horizontalMargin: spaceLg,
        columnSpacing: spaceXl,
        decoration: BoxDecoration(
          border: Border.all(color: border),
          borderRadius: BorderRadius.circular(radiusLg),
        ),
      ),
      expansionTileTheme: const ExpansionTileThemeData(
        backgroundColor: Colors.transparent,
        collapsedBackgroundColor: Colors.transparent,
        textColor: textPrimary,
        collapsedTextColor: textPrimary,
        iconColor: accent,
        collapsedIconColor: textMuted,
        tilePadding: EdgeInsets.symmetric(horizontal: spaceLg),
        childrenPadding: EdgeInsets.fromLTRB(
          spaceLg,
          0,
          spaceLg,
          spaceLg,
        ),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.all(Radius.circular(radiusMd)),
          side: BorderSide(color: border),
        ),
        collapsedShape: RoundedRectangleBorder(
          borderRadius: BorderRadius.all(Radius.circular(radiusMd)),
          side: BorderSide(color: border),
        ),
      ),
      tooltipTheme: TooltipThemeData(
        decoration: BoxDecoration(
          color: surfaceElevated,
          border: Border.all(color: borderStrong),
          borderRadius: BorderRadius.circular(radiusSm),
          boxShadow: [
            BoxShadow(
              color: Colors.black.withValues(alpha: 0.5),
              blurRadius: 16,
              offset: const Offset(0, 6),
            ),
          ],
        ),
        textStyle: _textTheme.labelMedium!.copyWith(color: textPrimary),
        padding: const EdgeInsets.symmetric(
          horizontal: spaceMd,
          vertical: spaceSm,
        ),
        waitDuration: const Duration(milliseconds: 450),
        showDuration: const Duration(seconds: 3),
      ),
      textSelectionTheme: const TextSelectionThemeData(
        cursorColor: accent,
        selectionColor: Color(0x55F2AC2D),
        selectionHandleColor: accent,
      ),
    );
  }

  static OutlineInputBorder _inputBorder(Color color, {double width = 1}) {
    return OutlineInputBorder(
      borderRadius: BorderRadius.circular(radiusMd),
      borderSide: BorderSide(color: color, width: width),
    );
  }

  static ButtonStyle _filledButtonStyle({
    required Color backgroundColor,
    required Color foregroundColor,
  }) {
    return ButtonStyle(
      backgroundColor: WidgetStateProperty.resolveWith((states) {
        if (states.contains(WidgetState.disabled)) {
          return surfaceElevated.withValues(alpha: 0.7);
        }
        return backgroundColor;
      }),
      foregroundColor: WidgetStateProperty.resolveWith((states) {
        if (states.contains(WidgetState.disabled)) return textMuted;
        return foregroundColor;
      }),
      overlayColor: _interactionOverlay(foregroundColor),
      shadowColor: const WidgetStatePropertyAll(Colors.transparent),
      elevation: const WidgetStatePropertyAll(0),
      minimumSize: const WidgetStatePropertyAll(Size(48, 48)),
      padding: const WidgetStatePropertyAll(
        EdgeInsets.symmetric(horizontal: spaceXl, vertical: spaceMd),
      ),
      textStyle: WidgetStatePropertyAll(_textTheme.labelLarge),
      shape: WidgetStatePropertyAll(
        RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(radiusMd),
        ),
      ),
    );
  }

  static WidgetStateProperty<Color?> _foreground(Color color) {
    return WidgetStateProperty.resolveWith((states) {
      if (states.contains(WidgetState.disabled)) return textMuted;
      return color;
    });
  }

  static WidgetStateProperty<Color?> _selectionFill(Color color) {
    return WidgetStateProperty.resolveWith((states) {
      if (states.contains(WidgetState.disabled)) {
        return textMuted.withValues(alpha: 0.45);
      }
      if (states.contains(WidgetState.selected)) return color;
      return Colors.transparent;
    });
  }

  static WidgetStateProperty<Color?> _interactionOverlay(Color color) {
    return WidgetStateProperty.resolveWith((states) {
      if (states.contains(WidgetState.pressed)) {
        return color.withValues(alpha: 0.16);
      }
      if (states.contains(WidgetState.focused)) {
        return color.withValues(alpha: 0.14);
      }
      if (states.contains(WidgetState.hovered)) {
        return color.withValues(alpha: 0.08);
      }
      return Colors.transparent;
    });
  }
}
