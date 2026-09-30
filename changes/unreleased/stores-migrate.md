### Fixed

- **Review receipts recorded at the same time no longer drop each other.** The console
  and `magus diff --ack` both write the store; the later writer erased what the other
  had just recorded.
