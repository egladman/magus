# Image examples

Each file is a magusfile for a small project that builds an OCI image. A toolchain
never runs inside an image: a target compiles, and the image targets place what it
wrote. Each stage, layer, platform, index and push is its own target.

| Example                                    | Shows                                                                             |
| ------------------------------------------ | --------------------------------------------------------------------------------- |
| [static-go.buzz](static-go.buzz)           | a build target feeding an image target; scratch; one layer                        |
| [multi-arch.buzz](multi-arch.buzz)         | a target per platform, an Image Index that fans in, SBOM, provenance, push        |
| [multi-stage.buzz](multi-stage.buzz)       | TypeScript assets and a Go server as stages of one project                        |
| [node-typescript.buzz](node-typescript.buzz) | one layer per target, so a code edit reuses the dependency layer                |
| [distroless.buzz](distroless.buzz)         | a minimal root filesystem written as files, with a sticky /tmp and a user's home |
| [config-overlay.buzz](config-overlay.buzz) | a pinned base with configuration files and config on top                          |
| [extend-base.buzz](extend-base.buzz)       | a pinned runtime base plus a binary                                               |

The build stages call `go` and `pnpm` through `proc\exec` so each file type-checks on its
own with `magus buzz --check`. In a project, bind the spell instead and call its op:
`go["go-build"](ctx.withEnv(image\goEnv(PLATFORM)), {"args": [...]})`.

## Multi-stage across projects

When a stage belongs to another team or toolchain, give it its own project. A
cross-project `needs` finishes before the consuming target starts, so the stage's
declared output is an ordinary input of the image target, which then lists it in
`readsFiles`:

```buzz
// web/magusfile.buzz builds out/static; server/magusfile.buzz embeds it into
// out/linux-amd64/server. This is image/magusfile.buzz.
import "magus";
import "fs";
import "image";
import "project/../server";

export fun image_layout(ctx: magus\Context, args: [str]) > void !> any {
    ctx.needs(server.build);
    ctx.readsFiles([server.file("out/linux-amd64/server")]);
    ctx.writesFiles(["out/image/**"]);
    image\assemble(image\Spec{
        scratch = true,
        platform = image\Platform{ os = image\OS.linux, architecture = image\Architecture.amd64 },
        files = [image\File{ source = "../server/out/linux-amd64/server", path = "/server", mode = fs\mode("0755") }],
        config = image\Config{ entrypoint = ["/server"] },
    }, layout: "out/image");
}
```

In ONE project, the same read trips MGS4010: a same-project target needed from a
cacheable target runs inside that target's cache entry, after its key is taken. There
the image target lists the stage's sources instead, as
[multi-stage.buzz](multi-stage.buzz) does.

What the module cannot express, and why: [Assemble an OCI image](../../../docs/guides/setup/assemble-oci-image.md).
