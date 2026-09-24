### Security

- **`magus queue apply` checks the plan against what it reads itself.** A plan naming
  another base or remote, a base commit the base lacks, or a stack base that is not
  the reviewed head beneath stops applying (MGS3025): a forged stack base could merge
  a revert of the base. Apply writes the squash message itself.
