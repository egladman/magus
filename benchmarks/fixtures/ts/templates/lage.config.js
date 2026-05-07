/** @type {import("@microsoft/lage").ConfigOptions} */
module.exports = {
  pipeline: {
    build: ["^build"],
  },
  cacheOptions: {
    outputGlob: ["dist/**"],
  },
};
