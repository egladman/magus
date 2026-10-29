### Added

- **The python spell reports the projects its markers form.** Each directory with a
  `pyproject.toml`, `setup.py` or `setup.cfg` is a project, with `lint`, `format`, `test`
  and `build` wired from ruff, pytest and `[build-system]`, and uv sources as
  dependencies. Unwired tools, unread locks and invalid TOML are gaps naming the file.
