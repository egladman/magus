# The analysis side of the benchmark. Eli's constraint: no Python runs on the
# host, so the extractor, the judge harness and the statistics all execute here.
#
# Nothing is installed and nothing is copied in: the image pins the interpreter,
# the analysis code and the run artifacts are bind-mounted at run time, and the
# analysis stays stdlib-only. Adding a pip install here is a design change, not
# a convenience: it makes the numbers depend on a resolver.
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PYTHONHASHSEED=0

WORKDIR /work
ENTRYPOINT ["python3"]
CMD ["--version"]
