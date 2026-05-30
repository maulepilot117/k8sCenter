// Pure helper shared by wizards that render a repeating-row sub-form
// whose empty rows are stripped before send (Service ports, Deployment
// container ports, etc.). The backend and `validateLocally` key
// `ports[N]` errors against the *stripped* list, so the UI must map a
// display-row index back to that server/stripped index to land the
// error on the row the operator actually filled.
//
// Kept widget-agnostic via an `isEmptyAt` predicate so callers with
// different row types (ServicePort, ContainerPortData, …) share one
// tested implementation instead of duplicating the counting loop.

/// Maps a display-row index to the server/stripped index that the
/// backend and `validateLocally` key errors on (empty rows are dropped
/// before send).
///
/// Returns null when [displayIndex] is out of range or the row at it is
/// empty. Otherwise returns the count of non-empty rows in
/// `[0, displayIndex)`.
int? serverIndexForRow(
  int rowCount,
  bool Function(int index) isEmptyAt,
  int displayIndex,
) {
  if (displayIndex < 0 || displayIndex >= rowCount) return null;
  if (isEmptyAt(displayIndex)) return null;
  var count = 0;
  for (var i = 0; i < displayIndex; i++) {
    if (!isEmptyAt(i)) count++;
  }
  return count;
}
