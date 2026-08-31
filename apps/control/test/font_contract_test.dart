import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:jastreamer_control/control_theme.dart';

void main() {
  test('bundled Korean font is bound before Flutter Web starts', () {
    // Given: machine-consumed Flutter and Web configuration.
    final pubspec = File('pubspec.yaml').readAsStringSync();
    final bootstrap = File('web/flutter_bootstrap.js').readAsStringSync();

    // When / Then: all weights use the vendored family and fallback is local.
    expect(
      RegExp(r'asset: assets/fonts/noto_sans_kr/NotoSansKR-wght.ttf')
          .allMatches(pubspec)
          .length,
      9,
    );
    expect(bootstrap,
        contains('fontFallbackBaseUrl: "assets/font-fallback-disabled/"'));
    expect(bootstrap, isNot(contains('fonts.gstatic')));
  });

  testWidgets('representative Korean UI text inherits the bundled family',
      (tester) async {
    // Given: representative Korean text rendered through the real Control theme.
    await tester.pumpWidget(
      MaterialApp(
        theme: controlTheme(),
        home: const Scaffold(body: Text(controlKoreanFontProbeText)),
      ),
    );

    // When: the shipped text surface resolves its inherited body style.
    final context = tester.element(find.text(controlKoreanFontProbeText));

    // Then: Korean uses the repository-controlled family, not system fallback.
    expect(
        Theme.of(context).textTheme.bodyMedium?.fontFamily, controlFontFamily);
  });
}
