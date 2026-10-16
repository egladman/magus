module github.com/egladman/magus/hack/embed

go 1.26.5

require (
	github.com/egladman/magus/libs/xdg v0.1.0
	github.com/gomlx/compute v0.1.6
	github.com/gomlx/go-xla v0.4.5
	github.com/gomlx/gomlx v0.28.8
	golang.org/x/text v0.35.0
)

require (
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
)

replace github.com/egladman/magus/libs/xdg => ../../libs/xdg
