### Fixed

- **`magus job fork --checkout` waits for the build budget instead of removing the checkout.** The bootstrap script seats each `magus run` it starts behind `magus status --wait`, says what it waits on, and gives up after 30 minutes naming who holds the budget, where a busy machine used to refuse the run with MGS3009.
