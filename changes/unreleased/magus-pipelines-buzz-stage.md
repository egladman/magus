### Added

- **A `pipe` Buzz module makes `magus buzz <script>` a stage of a magus pipe.** A
  script reads the records of the run before it with `pipe\more`, `pipe\next` and
  `pipe\all`, and `pipe\emit` writes records for the stage after it, so `magus run test .
  | magus buzz failures.buzz` replaces `| grep FAIL`. Its exit status counts like any
  stage's.
