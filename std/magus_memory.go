//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/memory"
	"github.com/egladman/magus/types"
)

// memoryRoot names the store `magus memory` and magus\memory also read: it is
// keyed by repository, so every worktree of one repository shares it.
func memoryRoot(ctx context.Context, member string) (string, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return "", errNoWorkspace(member)
	}
	return ws.Root(), nil
}

// MagusListMemory backs magus\memory.list: {records, issues}. A malformed entry is an
// issue beside the readable records, never a failure of the listing, since the listing
// is where a person finds the bad entry to delete.
func MagusListMemory(ctx context.Context) (map[string]any, error) {
	root, err := memoryRoot(ctx, "memory.list")
	if err != nil {
		return nil, err
	}
	recs, issues, err := memory.Inspect(root)
	if err != nil {
		return nil, err
	}
	// Non-nil so an empty store lists [] rather than null.
	if recs == nil {
		recs = []memory.Record{}
	}
	if issues == nil {
		issues = []memory.Issue{}
	}
	return recordMap(map[string]any{"records": recs, "issues": issues})
}

// MagusGetMemory backs magus\memory.get. An absent name raises: a caller that asks for
// one entry by name has the wrong name, and list is how it finds the right one.
func MagusGetMemory(ctx context.Context, name string) (map[string]any, error) {
	root, err := memoryRoot(ctx, "memory.get")
	if err != nil {
		return nil, err
	}
	rec, err := memory.Get(root, strings.TrimSpace(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("magus\\memory.get: no entry named %q", name)
	}
	if err != nil {
		return nil, err
	}
	return recordMap(rec)
}

// MagusPutMemory backs magus\memory.put: create the entry name, or write the fields opts
// names on one that exists and keep every other. A key opts omits is untouched and a key
// present with an empty value is an explicit clear, the merge magus\job.put makes.
//
// opts takes type, status, refs ([str] of 'kind: target'), references ([str]), body,
// excerpt, and allow_missing (default true; false turns a mistyped name into an error
// instead of a second entry). Returns the stored entry.
func MagusPutMemory(ctx context.Context, name string, opts map[string]any) (map[string]any, error) {
	root, err := memoryRoot(ctx, "memory.put")
	if err != nil {
		return nil, err
	}
	rec := memory.Record{Name: strings.TrimSpace(name)}
	update := memory.UpdateOptions{AllowMissing: true}
	for k, v := range opts {
		s, isString := v.(string)
		switch k {
		case "type", "status", "body", "excerpt":
			if !isString {
				return nil, fmt.Errorf("magus\\memory.put: %s must be a string, got %T", k, v)
			}
			update.Mask = append(update.Mask, k)
		}
		switch k {
		case "type":
			rec.Type = memory.RecordType(strings.TrimSpace(s))
		case "status":
			rec.Status = strings.TrimSpace(s)
		case "body":
			rec.Body = s
		case "excerpt":
			rec.Excerpt = s
		case "refs":
			lines, err := memoryStrings(k, v)
			if err != nil {
				return nil, err
			}
			if rec.Refs, err = memory.ParseRefs(strings.Join(lines, "\n")); err != nil {
				return nil, err
			}
			update.Mask = append(update.Mask, k)
		case "references":
			if rec.References, err = memoryStrings(k, v); err != nil {
				return nil, err
			}
			update.Mask = append(update.Mask, k)
		case "allow_missing":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("magus\\memory.put: allow_missing must be a bool, got %T", v)
			}
			update.AllowMissing = b
		default:
			return nil, fmt.Errorf("magus\\memory.put: unknown option %q (want type, status, refs, references, body, excerpt, allow_missing)", k)
		}
	}
	stored, err := memory.Update(root, rec, update)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("magus\\memory.put: no entry named %q; drop allow_missing = false to create it, or find it with magus\\memory.list", rec.Name)
	}
	if err != nil {
		return nil, err
	}
	return recordMap(stored)
}

func memoryStrings(key string, v any) ([]string, error) {
	switch x := v.(type) {
	case []string:
		return x, nil
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("magus\\memory.put: %s[%d] must be a string, got %T", key, i, e)
			}
			out[i] = s
		}
		return out, nil
	}
	return nil, fmt.Errorf("magus\\memory.put: %s must be a list of strings, got %T", key, v)
}

// MagusDeleteMemory backs magus\memory.delete. The entry moves into the store's archive
// rather than being removed, and an absent name raises: a script that deletes a name it
// never checked is more often holding a typo than running twice.
func MagusDeleteMemory(ctx context.Context, name string) error {
	root, err := memoryRoot(ctx, "memory.delete")
	if err != nil {
		return err
	}
	if _, err := memory.Delete(root, strings.TrimSpace(name)); err != nil {
		return fmt.Errorf("magus\\memory.delete: %w", err)
	}
	return nil
}

// MagusVerifyMemory backs magus\memory.verify: {records, issues} for malformed entries,
// broken links between entries, and output refs this checkout can no longer reopen.
func MagusVerifyMemory(ctx context.Context) (map[string]any, error) {
	root, err := memoryRoot(ctx, "memory.verify")
	if err != nil {
		return nil, err
	}
	cd, ok := types.WorkspaceFromContext(ctx).(workspaceCacheDir)
	if !ok {
		return nil, errors.New("magus\\memory.verify: this workspace has no cache directory to resolve output refs against")
	}
	store := cache.NewOutputStore(cd.CacheDir())
	report, err := memory.Verify(root, func(ref memory.Ref) error {
		if ref.Kind != memory.RefKindOutput {
			return nil
		}
		_, err := store.DescriptorByRef(ref.Target)
		return err
	})
	if err != nil {
		return nil, err
	}
	if report.Issues == nil {
		report.Issues = []memory.Issue{}
	}
	return recordMap(report)
}
