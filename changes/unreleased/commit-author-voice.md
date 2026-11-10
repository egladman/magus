### Added

- **The commit guard refuses a message that names its own author.** A subject such as "record Eli's picks" is denied when the committer's `user.name`, its first word or a possessive of either appears in it. The denial says to write it as yourself or drop the name.
