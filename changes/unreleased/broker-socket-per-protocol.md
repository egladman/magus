### Changed

- **Each broker wire protocol has its own socket, `broker-<protocol>.sock`.** A build that
  speaks another protocol runs its own broker instead of meeting a refusal and running
  unarbitrated (MGS3048). The two brokers share no machine budget, and `magus broker status`
  and a starting broker warn, naming the other socket, while one is live.
