# The trial image for one SWE-bench instance: the instance's own image (which is
# what holds the repo's Python environment at /testbed) plus the two things an
# agent session needs that it lacks, the claude CLI and the magus under test.
# The base is the registry build for the host's architecture (see lib.sh), so
# the node tarball follows NODE_ARCH and nothing here runs emulated.
#
# The magus binary is COPIED in, never built here: the runner hands it over as
# --magus-binary, which is how one instance can be run against two builds.
ARG BASE
FROM ${BASE}
ARG NODE_VERSION=24.21.0
ARG NODE_ARCH=x64
ARG CLAUDE_CODE_VERSION=2.1.212

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl jq patch xz-utils \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz" \
    | tar -xJ -C /usr/local --strip-components=1 \
        --exclude=CHANGELOG.md --exclude=LICENSE --exclude=README.md \
    && npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
    && npm cache clean --force

COPY magus /usr/local/bin/magus

# The eval script activates the conda env itself; an agent's shell and magus's
# proc\exec see the same interpreter only if the env is on PATH up front.
ENV PATH=/opt/miniconda3/envs/testbed/bin:/opt/miniconda3/bin:${PATH}
WORKDIR /testbed
