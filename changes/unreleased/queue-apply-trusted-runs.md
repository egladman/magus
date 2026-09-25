### Security

- **`magus queue apply` follows only the base's own validation run.** A pull
  request's run executes its own copy of the queue workflow and could upload a forged
  plan and verdicts that merged ungated. Apply now refuses any run but `--workflow`
  started by a push or dispatch on `--base` of its repository (MGS3027), and
  `queue-apply.yaml` dispatches main's run instead.
