### Changed

- **Buzz module declarations can import another module and name its types qualified.**
  `fs\Path` in proc's declarations resolves through proc's own `import "fs";`.
  `object<mod\Proto>` parses. The checker now refuses an enum where a builtin method
  takes a str (BZZ1005), and an object literal leaving a field without a default unset.
