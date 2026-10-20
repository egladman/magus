### Changed

- **The queue hook's exit 75 loop, the HTTP transport and the Buzz `os\retry` loop stay separate.** Each differs in what it retries, what it returns or who sets the policy.
