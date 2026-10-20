### Fixed

- **`magus job exit` removes a checkout whose only changes are the tool rows a wired
  harness's install wrote.** `magus describe file` leaves those files unclaimed, so the
  checkout was kept as holding uncommitted work. They are now restored like any other
  file magus writes; a change to any other file still keeps the checkout.
