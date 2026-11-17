### Fixed

- **An undecodable request blob in the activity store is named on `magus diff`.** It
  used to drop out of the agent record unsaid, and an unreadable session store is named
  there too.
- **Every JSON Lines store reports an undecodable line as MGS3050 and a leftover older
  store as MGS3049.** Only the job, forecast and log stores did.
