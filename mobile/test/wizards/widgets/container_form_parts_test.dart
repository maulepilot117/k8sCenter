// Tests for the shared container form data records — env vars +
// container ports.

import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/wizards/widgets/container_form_parts.dart';

void main() {
  group('envVarsAsJson', () {
    test('drops empty rows and keeps {name, value}', () {
      final out = envVarsAsJson(const [
        EnvVarData(name: 'A', value: '1'),
        EnvVarData(),
        EnvVarData(name: 'B', value: '2'),
      ]);
      expect(out.length, 2);
      expect(out[0], {'name': 'A', 'value': '1'});
      expect(out[1], {'name': 'B', 'value': '2'});
    });
  });

  group('containerPortsAsJson', () {
    test('drops empty rows and includes name/protocol when set', () {
      final out = containerPortsAsJson(const [
        ContainerPortData(
            name: 'http', containerPort: 80, protocol: 'TCP'),
        ContainerPortData(),
        ContainerPortData(containerPort: 443, protocol: 'TCP'),
      ]);
      expect(out.length, 2);
      expect(out[0]['name'], 'http');
      expect(out[0]['containerPort'], 80);
      expect(out[1].containsKey('name'), false);
      expect(out[1]['containerPort'], 443);
    });
  });
}
