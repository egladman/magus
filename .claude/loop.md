Babysit this repository's open pull requests. Follow the babysit-pull-requests skill.

Each wake:

1. Run `./magus buzz hack/ci/pull-requests.buzz -- status`, then `-- apply`.
2. Run `./magus session attention` and `./magus ls jobs`. Clear a block only the root can (rebuild `./magus`, rebind a lease). Leave the rest.
3. Never run a `merge` command and never push. Hand each to the person.

Stop when every record reads `merged`. Otherwise schedule the next wake in about 20 minutes.
