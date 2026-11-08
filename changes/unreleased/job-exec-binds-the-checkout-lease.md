### Fixed

- **`job exec` binds its checkout to the job.** The broker named the lease for
  none of a day's 104 recorded owners, since a bare `job exec` bound the session
  through the guard and wrote no checkout record. `job exit` releases the record.
