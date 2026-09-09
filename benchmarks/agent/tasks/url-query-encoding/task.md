# Query strings come out malformed

`packages/platform/http` builds a request url from a base and a bag of query
parameters. A value containing a space, an ampersand, an equals sign or a slash
comes out unescaped, so the url is wrong and anything downstream mis-parses it.

Fix it. Both the keys and the values have to be percent-encoded. Everything else
about the function stays as it is: an absent or empty query returns the base
unchanged, and the parameters stay sorted by key.

The package's own tests cover this area.
