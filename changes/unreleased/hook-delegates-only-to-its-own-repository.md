### Security

- **The agent hook runs only a binary of the session's own repository.** The glue starts the
  session checkout's `./magus`, whatever the call's cwd or workdir. That binary hands a call
  on, or re-executes, only into a linked checkout of its repository. A clone or a planted
  directory is judged by the running binary, and the verdict says why.
