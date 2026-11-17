package render

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"

	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/codec"
)

// outputWriter renders json, yaml, jsonl and template through magus's one JSON codec,
// so -o json and a template see the same wire form every other byte magus writes has.
var outputWriter = output.Writer{
	Codec: output.Codec{Marshal: codec.Marshal, Unmarshal: codec.Unmarshal},
	Funcs: TemplateFuncs(),
}

// WriteFormatted renders v in opts' structured format to w.
func WriteFormatted(w io.Writer, opts output.Options, v any) error {
	return outputWriter.Write(w, opts, v)
}

// excludedSprigFuncs lists sprig entries absent from magus templates (non-hermetic or dangerous).
// HermeticTxtFuncMap already drops env/expandenv/getHostByName; this list adds the rest.
var excludedSprigFuncs = []string{
	// non-deterministic
	"now", "date", "dateInZone", "dateModify", "mustDateModify",
	"toDate", "unixEpoch", "htmlDate", "htmlDateInZone",
	"uuidv4",
	// crypto — no business in a build-tool template
	"genPrivateKey", "genCA", "genSelfSignedCert", "genSignedCert",
	"derivePassword", "htpasswd", "bcrypt",
	// confusing in -o context
	"fail",
}

// TemplateFuncs returns the FuncMap exposed to -o template= bodies.
func TemplateFuncs() template.FuncMap {
	f := sprig.HermeticTxtFuncMap()
	for _, name := range excludedSprigFuncs {
		delete(f, name)
	}
	// Preserve magus's original bindings; magus's list-first join wins over sprig's
	// reversed (sep-first) join. templateJoin (not strings.Join) so it also handles
	// the []any that json-normalization produces for list fields.
	f["join"] = templateJoin
	f["upper"] = strings.ToUpper
	f["lower"] = strings.ToLower
	f["trim"] = strings.TrimSpace
	return f
}

// templateJoin joins a list's elements with sep, keeping magus's list-first arg order
// ({{join .list ","}}). Unlike strings.Join it accepts ANY slice/array ([]string
// from sprig helpers, and the []any that -o template's json-normalized data yields
// for list fields), stringifying each element. Non-slices stringify whole.
func templateJoin(list any, sep string) string {
	if list == nil { // an absent/null list field joins to empty, like strings.Join(nil, sep)
		return ""
	}
	rv := reflect.ValueOf(list)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return fmt.Sprint(list)
	}
	parts := make([]string, rv.Len())
	for i := range parts {
		parts[i] = fmt.Sprint(rv.Index(i).Interface())
	}
	return strings.Join(parts, sep)
}
