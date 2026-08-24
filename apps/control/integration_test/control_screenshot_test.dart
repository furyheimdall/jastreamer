import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:jastreamer_control/control_application.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('renders the control room entry surface', (tester) async {
    await tester.pumpWidget(const ControlApp());
    await tester.pumpAndSettle();
    expect(find.text('Control room'), findsOneWidget);
  });
}
