//go:build !buzz_safe && !buzz_unsafe

package vm

// HeapProfile counts heap slots by object kind.
//
// The question this answers, before anyone writes a collector: WHAT accumulates.
// A heap that is 95% strings wants a different fix from one that is mostly lists
// or closure cells, and "the heap is enormous" does not distinguish them. Walking
// the table is O(n) and this is a diagnostic, not a hot path.
//
// Keys are the kind names Value.Kind() reports, so a reader can map a count back
// to something they can search for in their own source.
func HeapProfile() map[string]int {
	s := gHeapPtr.Load()
	if s == nil {
		return nil
	}
	out := make(map[string]int, 8)
	for _, o := range *s {
		if o == nil {
			continue
		}
		out[kindName(o.heapKind())]++
	}
	return out
}

// kindName maps a heap tag to the name Value.Kind() uses.
func kindName(t valueTag) string {
	switch t {
	case tagStr:
		return "str"
	case tagList:
		return "list"
	case tagMap:
		return "map"
	case tagFun:
		return "fun"
	case tagObject:
		return "object"
	case tagObjectDef:
		return "objectDef"
	case tagEnumDef:
		return "enumDef"
	case tagEnumVal:
		return "enumVal"
	case tagCell:
		return "cell"
	case tagFib:
		return "fiber"
	case tagRange:
		return "range"
	case tagIterState:
		return "iterState"
	case tagUD:
		return "userdata"
	case tagDirect:
		return "direct"
	default:
		return "other"
	}
}
