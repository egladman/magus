### Changed

- **`magus\graph().precedents` reads `knowledge.conformance`.** Its gates decide which rows are established, and an imported-packages row's ceiling is the layer's committed or configured value; a layer with neither gets no fan-out row.
