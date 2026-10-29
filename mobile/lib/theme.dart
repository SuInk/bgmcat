import 'package:flutter/material.dart';

/// 与 web 端深色主题同一套色：主题色 #0041fd 朝白提亮后的 #4b83ff，底色偏紫的深灰。
class AppColors {
  static const accentSeed = Color(0xFF0041FD);
  static const accent = Color(0xFF4B83FF);
  static const accentContrast = Color(0xFF0B0F1C);
  static const background = Color(0xFF16131A);
  static const card = Color(0xFF201C26);
  static const panel = Color(0xFF2A2531);
  static const ink = Color(0xFFF1EAEF);
  static const inkSecondary = Color(0xFFC4BAC2);
  static const muted = Color(0xFF8D8290);
  static const line = Color(0x1FE2E8FF);
  static const ok = Color(0xFF3FBD8A);
  static const warn = Color(0xFFE0A13C);
  static const err = Color(0xFFE8666A);
}

ThemeData buildTheme() {
  final scheme = ColorScheme.fromSeed(
    seedColor: AppColors.accentSeed,
    brightness: Brightness.dark,
  ).copyWith(
    primary: AppColors.accent,
    onPrimary: AppColors.accentContrast,
    surface: AppColors.background,
    onSurface: AppColors.ink,
    onSurfaceVariant: AppColors.inkSecondary,
    surfaceContainerLowest: AppColors.background,
    surfaceContainerLow: AppColors.card,
    surfaceContainer: AppColors.card,
    surfaceContainerHigh: AppColors.panel,
    surfaceContainerHighest: AppColors.panel,
    outline: AppColors.muted,
    outlineVariant: AppColors.line,
    error: AppColors.err,
  );
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    scaffoldBackgroundColor: AppColors.background,
    appBarTheme: const AppBarTheme(
      backgroundColor: AppColors.background,
      surfaceTintColor: Colors.transparent,
      centerTitle: false,
      titleTextStyle: TextStyle(fontSize: 20, fontWeight: FontWeight.w700, color: AppColors.ink),
    ),
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: AppColors.card,
      indicatorColor: AppColors.accent.withValues(alpha: 0.18),
      surfaceTintColor: Colors.transparent,
      labelTextStyle: WidgetStateProperty.resolveWith((states) => TextStyle(
            fontSize: 12,
            color: states.contains(WidgetState.selected) ? AppColors.accent : AppColors.muted,
          )),
      iconTheme: WidgetStateProperty.resolveWith((states) => IconThemeData(
            color: states.contains(WidgetState.selected) ? AppColors.accent : AppColors.muted,
          )),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: AppColors.panel,
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide.none),
      contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      hintStyle: const TextStyle(color: AppColors.muted),
    ),
    chipTheme: ChipThemeData(
      backgroundColor: AppColors.panel,
      selectedColor: AppColors.accent.withValues(alpha: 0.22),
      side: BorderSide.none,
      labelStyle: const TextStyle(fontSize: 13, color: AppColors.inkSecondary),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(20)),
      showCheckmark: false,
    ),
    snackBarTheme: const SnackBarThemeData(behavior: SnackBarBehavior.floating),
    dividerTheme: const DividerThemeData(color: AppColors.line, space: 1),
  );
}
