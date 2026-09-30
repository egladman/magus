package bindings

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// magusContextSource mirrors the two maps buildTargetContext hands a target. A member
// taking any number of arguments is `any`, the type moduledecls gives a variadic host
// method, since a Buzz fun has no variadic parameter. Exec carries no declaration
// members, so `ctx.withEnv({...}).readsFiles(...)` fails the check rather than at run
// time. The removed inputs/outputs/updates are left undeclared for the same reason.
const magusContextSource = `export object Exec {
    env: {str: str}? = null,
    cwd: str? = null,

    extern fun withEnv(env: {str: str}) > Exec;
    extern fun withCwd(dir: str) > Exec;
}

export object Context {
    needs: any = null,
    glob: any = null,
    readsFiles: any = null,
    writesFiles: any = null,
    modifiesExistingFiles: any = null,
    envInputs: any = null,
    observes: any = null,

    extern fun uses(resource: any, body: fun () > void) > void;
    extern fun hasCharm(name: str) > bool;
    extern fun narrowed() > bool;
    extern fun withEnv(env: {str: str}) > Exec;
    extern fun withCwd(dir: str) > Exec;
}
`

type magusRecord struct {
	name  string
	value vm.Value
	enum  bool
}

// magusRecords holds a runtime definition for every plain record and enum the magus
// mirrors declare. The module is native, so no import executes its declarations the
// way a Buzz-source module's are, and `magus\DirsOptions{...}` would otherwise
// type-check and then fail with "unknown object type". A record definition has no
// methods and no static state, so one set is shared by every session.
var magusRecords = sync.OnceValues(func() ([]magusRecord, error) {
	src := magusDeclSource()
	prog, err := buzz.ParseEmbedded(src)
	if err != nil {
		return nil, err
	}
	// Only the records and enums run. An object with methods is a namespace or the
	// target context: the host provides it, and an extern method cannot be compiled.
	lines := strings.Split(src, "\n")
	var body strings.Builder
	var out []magusRecord
	seen := map[string]bool{}
	for i, stmt := range prog.Stmts {
		var r magusRecord
		switch d := stmt.(type) {
		case *ast.ObjectDecl:
			if !d.IsExported || d.IsProtocol || len(d.Methods) > 0 || len(d.StaticFields) > 0 {
				continue
			}
			r.name = d.Name
		case *ast.EnumDecl:
			if !d.IsExported {
				continue
			}
			r.name, r.enum = d.Name, true
		default:
			continue
		}
		if seen[r.name] {
			continue
		}
		seen[r.name] = true
		end := len(lines)
		if i+1 < len(prog.Stmts) {
			end = ast.NodePos(prog.Stmts[i+1]).Line - 1
		}
		body.WriteString(strings.Join(lines[ast.NodePos(stmt).Line-1:end], "\n"))
		body.WriteByte('\n')
		out = append(out, r)
	}
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	if err := sess.Exec(context.Background(), body.String()); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].value = sess.GetGlobal(out[i].name)
		if out[i].value.IsNull() {
			return nil, fmt.Errorf("magus declaration %s defined no runtime value", out[i].name)
		}
	}
	return out, nil
})

// magusDeclSource is the whole magus\ declaration text: the generated mirrors plus the
// ones the generator cannot reach.
func magusDeclSource() string {
	decls, ok := spell.ModuleDecls("magus")
	if !ok {
		panic("bindings: generated magus declarations are missing; run `magus run generate`")
	}
	return decls + "\n" + magusUndeclaredTypeSource
}

// defineMagusRecords binds every magus record and enum under its bare name, which is
// what `magus\T{...}` constructs at run time, and sets each enum on the magus module so
// `magus\E.case` and an inferred `.case` argument resolve. A name the session already
// binds keeps its binding, as a program's own type outranks a mirror in the checker.
// Register the magus namespace first, or the enums miss the module.
//
// Declarations that do not run leave the records annotate-only rather than panic: the
// binary that regenerates stale declarations loads this same code.
// TestMagusRecordsAreConstructible is what fails.
func defineMagusRecords(sess *buzz.Session) {
	records, err := magusRecords()
	if err != nil {
		return
	}
	globals := sess.Globals()
	mod, hasModule := sess.NativeModule("magus")
	for _, r := range records {
		if _, bound := globals[r.name]; !bound {
			sess.SetGlobal(r.name, r.value)
		}
		if hasModule && r.enum {
			if _, taken := mod.MapGet(r.name); !taken {
				mod.MapSet(r.name, r.value)
			}
		}
	}
}
