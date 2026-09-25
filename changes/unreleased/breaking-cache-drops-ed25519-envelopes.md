### Security

- **Breaking: the remote cache refuses pre-domain `ed25519` signature envelopes.** They
  signed the manifest alone, so a store could replay a genuine entry with its log and
  descriptor swapped or stripped. Every magus from 0.4.0 signs the domain-separated form;
  an entry signed by 0.3.x is now a miss and rebuilds.
