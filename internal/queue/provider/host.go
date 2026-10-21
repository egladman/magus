package provider

import (
	"context"
	"embed"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"

	"github.com/egladman/magus/internal/queue/types"
)

// The records a script's ops take and return, generated from the boundary registry, leaf
// first in queueTypeFiles because a mirror names the mirrors it carries.
//
//go:generate go run ../../../cmd/magus-utils types -type Ack -out gen/types/ack.buzz
//go:generate go run ../../../cmd/magus-utils types -type Person -out gen/types/person.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueIntegration -out gen/types/integration.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueRequiredCheck -out gen/types/requiredcheck.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueSetting -out gen/types/setting.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueApp -out gen/types/app.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueSetupStep -out gen/types/setupstep.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueSetup -out gen/types/setup.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueSetupRefusal -out gen/types/setuprefusal.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueCapabilities -out gen/types/capabilities.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueDescribeResult -out gen/types/describeresult.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueChange -out gen/types/change.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueMergedChange -out gen/types/mergedchange.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueUnqueuedChange -out gen/types/unqueuedchange.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueClosedChange -out gen/types/closedchange.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueChanges -out gen/types/changes.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueApproval -out gen/types/approval.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueCheck -out gen/types/check.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueGreenChange -out gen/types/greenchange.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueMergeAnswer -out gen/types/mergeanswer.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueRunOrigin -out gen/types/runorigin.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueArtifact -out gen/types/artifact.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueArtifactListing -out gen/types/artifactlisting.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueReview -out gen/types/review.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueReviews -out gen/types/reviews.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueuePinnedChange -out gen/types/pinnedchange.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueReproduction -out gen/types/reproduction.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueDescribeRequest -out gen/types/describerequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueListChangesRequest -out gen/types/listchangesrequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueApprovalRequest -out gen/types/approvalrequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueRequiredChecksRequest -out gen/types/requiredchecksrequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueListGreenRequest -out gen/types/listgreenrequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueStatusRequest -out gen/types/statusrequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueRetargetRequest -out gen/types/retargetrequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueMergeRequest -out gen/types/mergerequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueKickBackRequest -out gen/types/kickbackrequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueMarkRequest -out gen/types/markrequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueListArtifactsRequest -out gen/types/listartifactsrequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueReviewsRequest -out gen/types/reviewsrequest.buzz
//go:generate go run ../../../cmd/magus-utils types -type QueueDismissReviewRequest -out gen/types/dismissreviewrequest.buzz
//go:embed gen/types/*.buzz
var queueTypes embed.FS

var queueTypeFiles = []string{
	"ack", "person", "integration", "requiredcheck", "setting", "app", "setupstep", "setup", "setuprefusal",
	"capabilities", "describeresult", "change", "mergedchange", "unqueuedchange", "closedchange", "changes", "approval", "check",
	"greenchange", "mergeanswer", "runorigin", "artifact", "artifactlisting", "review", "reviews", "pinnedchange",
	"reproduction", "describerequest", "listchangesrequest", "approvalrequest", "requiredchecksrequest",
	"listgreenrequest", "statusrequest", "retargetrequest", "mergerequest", "kickbackrequest", "markrequest",
	"listartifactsrequest", "reviewsrequest", "dismissreviewrequest",
}

// typesModule is the import path of the records' module, source only like magus/spell.
const typesModule = "mergequeue/types"

// typesSource is every record's mirror in queueTypeFiles order.
func typesSource() string {
	parts := make([]string, len(queueTypeFiles))
	for i, name := range queueTypeFiles {
		src, err := queueTypes.ReadFile("gen/types/" + name + ".buzz")
		if err != nil {
			panic(fmt.Sprintf("queue provider: the generated mirror %s.buzz is not embedded: %v", name, err))
		}
		parts[i] = string(src)
	}
	return strings.Join(parts, "\n")
}

// hostDecls types the host module for the checker; the natives below are what run.
const hostDecls = `export extern fun request(method: str, url: str, body: str = "", headers: {str: str} = {<str: str>}) > {str: any} !> any;
export extern fun codeSpan(text: str) > str !> any;
export extern fun codeBlock(text: str) > str !> any;
`

// maxBody bounds a response a provider reads: API answers are small, and an
// unbounded read is a memory hazard for a job that holds a write credential.
const maxBody = 32 << 20

var client = &http.Client{Timeout: 60 * time.Second}

var hostModule = buzz.Module{
	Name:   "mergequeue",
	Labels: []string{"host"},
	Bind: func(s *buzz.Session, _ buzz.ModuleEnv) error {
		m := vm.NewMap()
		m.MapSet("request", vm.DirectValue("mergequeue.request", request))
		m.MapSet("codeSpan", vm.DirectValue("mergequeue.codeSpan", literal("codeSpan", types.CodeSpan)))
		m.MapSet("codeBlock", vm.DirectValue("mergequeue.codeBlock", literal("codeBlock", types.CodeBlock)))
		s.SetNativeModule("mergequeue", m)
		s.SetModuleDecls("mergequeue", hostDecls)
		s.SetModuleDecls(typesModule, typesSource())
		return nil
	},
}

// literal binds render, which shows untrusted text literally in Markdown, as a native.
func literal(name string, render func(string) string) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) != 1 || !args[0].IsStr() {
			return vm.Null, fmt.Errorf("mergequeue.%s: want (text: str)", name)
		}
		return vm.StrValue(render(args[0].AsString())), nil
	}
}

func request(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 2 || !args[0].IsStr() || !args[1].IsStr() {
		return vm.Null, fmt.Errorf("mergequeue.request: want (method: str, url: str, body: str, headers: {str: str})")
	}
	var body io.Reader
	if len(args) > 2 && args[2].IsStr() && args[2].AsString() != "" {
		body = strings.NewReader(args[2].AsString())
	}
	req, err := http.NewRequestWithContext(ctx, args[0].AsString(), args[1].AsString(), body)
	if err != nil {
		return vm.Null, fmt.Errorf("mergequeue.request: %w", err)
	}
	if len(args) > 3 && args[3].IsMap() {
		for _, k := range args[3].MapKeys() {
			if v, ok := args[3].MapGet(k); ok && v.IsStr() {
				req.Header.Set(k, v.AsString())
			}
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return vm.Null, fmt.Errorf("mergequeue.request: %s %s: %w", req.Method, req.URL.Redacted(), err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return vm.Null, fmt.Errorf("mergequeue.request: read %s: %w", req.URL.Redacted(), err)
	}
	// A cut body would read as a shorter, valid answer: fewer pull requests, fewer reviews.
	if len(data) > maxBody {
		return vm.Null, fmt.Errorf("mergequeue.request: %s %s: response larger than %d bytes", req.Method, req.URL.Redacted(), maxBody)
	}
	out := vm.NewMap()
	out.MapSet("status", vm.IntValue(int64(res.StatusCode)))
	out.MapSet("body", vm.StrValue(string(data)))
	return out, nil
}
