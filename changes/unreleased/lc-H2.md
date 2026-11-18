### Changed

- **Breaking: the figure JSON spells its keys in snake_case.** `unscoped_why`,
  `graph_edges`, `hidden_edges` and `edge_marks` on a figure, and `imported_by`,
  `imports_indexed` and `called_by` on a directory. A figure written with the camelCase
  keys is refused. The Buzz field names do not change.
