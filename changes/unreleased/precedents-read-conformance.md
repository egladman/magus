### Changed

- **`magus\precedents` reads `knowledge.conformance`.** Its gates decide which rows are established, and a dep-fanout row's ceiling is the layer's committed or configured value; a layer with neither gets no fan-out row.
