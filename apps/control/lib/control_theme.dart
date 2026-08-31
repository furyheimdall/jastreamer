import 'package:flutter/material.dart';

const controlFontFamily = 'Noto Sans KR';
const controlKoreanFontProbeText = '안녕하세요 재생 목록';

abstract final class ControlColors {
  static const surfacePrimary = Color(0xFF121315);
  static const surfaceSecondary = Color(0xFF1B1D20);
  static const surfaceElevated = Color(0xFF24272B);
  static const surfaceDanger = Color(0xFF3A2424);
  static const textPrimary = Color(0xFFF4F0E8);
  static const textSecondary = Color(0xFFB4B0A8);
  static const accentPrimary = Color(0xFFE9A23B);
  static const statusSuccess = Color(0xFF62C391);
  static const statusWarning = Color(0xFFE9A23B);
  static const statusError = Color(0xFFE8756B);
  static const borderSubtle = Color(0xFF626872);
  static const progressTrack = Color(0xFF3D4147);
}

ThemeData controlTheme() => ThemeData(
      brightness: Brightness.dark,
      fontFamily: controlFontFamily,
      useMaterial3: true,
      scaffoldBackgroundColor: ControlColors.surfacePrimary,
      colorScheme: const ColorScheme.dark(
        primary: ControlColors.accentPrimary,
        surface: ControlColors.surfaceSecondary,
        error: ControlColors.statusError,
        onPrimary: ControlColors.surfacePrimary,
        onSurface: ControlColors.textPrimary,
      ),
      cardTheme: const CardThemeData(
        color: ControlColors.surfaceSecondary,
        elevation: 0,
        margin: EdgeInsets.zero,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.all(Radius.circular(12)),
          side: BorderSide(color: ControlColors.borderSubtle),
        ),
      ),
      focusColor: ControlColors.accentPrimary,
      filledButtonTheme: const FilledButtonThemeData(
        style: ButtonStyle(
          minimumSize: WidgetStatePropertyAll(Size(48, 48)),
        ),
      ),
      outlinedButtonTheme: const OutlinedButtonThemeData(
        style: ButtonStyle(
          minimumSize: WidgetStatePropertyAll(Size(48, 48)),
        ),
      ),
      textButtonTheme: const TextButtonThemeData(
        style: ButtonStyle(
          minimumSize: WidgetStatePropertyAll(Size(48, 48)),
        ),
      ),
      iconButtonTheme: const IconButtonThemeData(
        style: ButtonStyle(
          minimumSize: WidgetStatePropertyAll(Size(48, 48)),
        ),
      ),
      textTheme: const TextTheme(
        headlineMedium: TextStyle(fontSize: 28, fontWeight: FontWeight.w700),
        titleLarge: TextStyle(fontSize: 20, fontWeight: FontWeight.w700),
        titleMedium: TextStyle(fontSize: 16, fontWeight: FontWeight.w700),
        bodyMedium: TextStyle(fontSize: 15, color: ControlColors.textSecondary),
        labelMedium: TextStyle(fontSize: 12, fontWeight: FontWeight.w600),
      ),
    );
