// Active cluster id. PR-1c replaces this stub Notifier with the real
// implementation that lists registered clusters and supports admin-gated
// add/remove. PR-1b only needs the read side so the Dio interceptor can
// inject X-Cluster-ID.

import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Default cluster id matches the backend's local cluster identifier.
const String defaultClusterId = 'local';

class ActiveClusterController extends Notifier<String> {
  @override
  String build() => defaultClusterId;

  void setCluster(String id) {
    state = id;
  }
}

final activeClusterProvider = NotifierProvider<ActiveClusterController, String>(
  ActiveClusterController.new,
);
