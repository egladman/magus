module github.com/egladman/magus/libs/proto

go 1.25.0

// The codegen plugins are pinned HERE rather than in the root go.mod, which is what
// buf.gen.yaml's `local: [go, tool, protoc-gen-go]` resolves against once this
// directory is its own module. That closes a gap the root pinning left open: a plugin
// bump used to move only the root module, which is not an input to this project, so
// the committed .pb.go replayed from a cache built by the older plugin. Now the pin
// moves this module's own go.mod, which the generate target declares as an input.
tool (
	connectrpc.com/connect/cmd/protoc-gen-connect-go
	google.golang.org/protobuf/cmd/protoc-gen-go
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.11-20260709200747-435963d16310.1
	connectrpc.com/connect v1.19.0
	google.golang.org/protobuf v1.36.11
)
