### Added

- **The python spell reports the projects its markers form.** Every directory with a
  `pyproject.toml`, `setup.py` or `setup.cfg` is a project. A `pyproject.toml` that
  declares ruff (a dependency or `[tool.ruff]`) gets `lint` and `format`, one that
  declares pytest or a pytest plugin (or `[tool.pytest]`) gets `test`, and one with
  `[build-system]` gets `build`. uv path and workspace sources become dependencies.
  Python files and requirements files outside every project join their owning project
  as members. Whatever is not wired is reported against its file: a missing test runner
  or linter, a declared tool no op runs (mypy, black, tox), a poetry or pipenv lock uv
  does not read, poe, pdm and hatch tasks, unresolved sources, invalid TOML, and a
  `setup.py` or `setup.cfg` with no `pyproject.toml`.
