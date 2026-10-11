### Removed

- **Breaking: `magus diff --prompt`, with no replacement.** Every `magus diff` flag now serves
  the person who typed it, and agents read a review through the diff MCP tool. A script that
  passes `--prompt` fails with a usage error; drop the flag.
