### Changed

- **A read is judged the same whichever tool performs it.** magus restates a read tool's call as the `cat` or `sed -n` line it stands for, and `read-paging` no longer records a refused window as read, so retrying a refused window through another tool is refused too.
