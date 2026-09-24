### Changed

- **BREAKING: tokens carry their class and always expire; older ones are refused.**
  `mgo_` is the operator, `mgs_` a stored token (now in `tokens.d`), `mgl_` a share link,
  which the loopback daemon refuses. Stored tokens live at most 366 days and share links
  24 hours; longer, or `never`, is MGS9018. An old operator file is MGS9016, anything in
  `connectors.d` MGS9017.
