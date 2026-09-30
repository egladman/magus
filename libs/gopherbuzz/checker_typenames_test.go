package buzz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheck_UndefinedTypeName(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"parameter", `fun f(r: Nope) > void {}`, `undefined type "Nope"`},
		{"qualified parameter, unknown namespace", `fun f(r: host\Nope) > void {}`, `undefined type "host\Nope"`},
		{"return", `fun f() > [Nope] { return []; }`, `undefined type "Nope"`},
		{"yield", `fun f() > void *> Nope? {}`, `undefined type "Nope"`},
		{"variable", `final n: {str: Nope} = {<str: Nope>};`, `undefined type "Nope"`},
		{"local variable", `fun f() > void { final n: Nope? = null; }`, `undefined type "Nope"`},
		{"object field", `object O { r: Nope? = null }`, `undefined type "Nope"`},
		{"static field", `object O { static r: Nope? = null }`, `undefined type "Nope"`},
		{"method parameter", `object O { fun m(r: Nope) > void {} }`, `undefined type "Nope"`},
		{"lambda parameter", `final g = fun (r: Nope) > void {};`, `undefined type "Nope"`},
		{"function type", `fun f(g: fun (x: Nope) > int) > void {}`, `undefined type "Nope"`},
		{"extern", `extern fun f(r: Nope) > void;`, `undefined type "Nope"`},
		{"type parameter out of scope", `fun f::<T>() > void {} fun g(x: T) > void {}`, `undefined type "T"`},
	} {
		t.Run(tc.name, func(t *testing.T) { checkErr(t, tc.src, tc.want) })
	}
}

func TestCheck_UndefinedTypeNameCode(t *testing.T) {
	errs := checkSrc("final _x = 1;\nfun f(r: Nope, s: Nope) > void {}")
	assert.Equal(t, []typeError{{Line: 2, Col: 1, Code: UndefinedType, Msg: `undefined type "Nope"`}}, errs)
}

func TestCheck_TypeParametersAreDeclaredNames(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"function", `fun count::<T>(list: [T]) > int { return list.len(); } final _n = count::<int>([1, 2]);`},
		{"function return and yield", `fun gen::<T>(v: T) > T *> T? { return v; }`},
		{"extern", `extern fun pick::<K, V>(m: {K: V}) > V;`},
		{"object fields and methods", `object Box::<V> { v: V, static s: V? = null, fun get() > V { return this.v; } }`},
		{"method's own", `object Box { fun map::<U>(u: U) > U { return u; } }`},
		{"protocol method", `protocol P { fun get::<U>(u: U) > U; }`},
		{"lambda", `final id = fun::<E>(e: E) > E { return e; };`},
		{"enclosing function reaches a lambda", `fun keep::<T>(v: T) > fun () > T { return fun () > T { return v; }; }`},
		{"enclosing function reaches a local", `fun keep::<T>(v: T) > T { final w: T = v; return w; }`},
		{"function type annotation", `fun twice::<A>(lambda: fun::<C, D>(c: C) > D) > int { return 1; }`},
		{"keyword types", `fun f(r: rg, t: type, o: obj{ n: int }) > void {}`},
		{"declared later", `fun f(o: Later, e: Kind) > void {} object Later {} enum Kind { a }`},
	} {
		t.Run(tc.name, func(t *testing.T) { checkOK(t, tc.src) })
	}
}

func TestCheck_QualifiedTypeResolvesInItsNamespace(t *testing.T) {
	ctx := context.Background()
	eval := func(t *testing.T, src string) error {
		t.Helper()
		sess := NewSession(ctx, WithEmbedded())
		t.Cleanup(func() { _ = sess.Close() })
		sess.SetModuleDecls("host", `export object Site { file: str = "" }`)
		sess.SetModuleDecls("other", `export final n = 1;`)
		return sess.Exec(ctx, src)
	}
	require.NoError(t, eval(t, `import "host"; fun f(s: host\Site) > str { return s.file; }`))

	err := eval(t, `import "host"; fun f(s: host\Site) > str { return s.file; } final _s = f("x");`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `cannot pass str as argument "s" of type Site`)

	err = eval(t, `import "host"; object Site { n: int = 0 } fun f(s: host\Site) > int { return s.n; }`)
	require.Error(t, err, "host\\Site must not resolve to the local Site")
	assert.Contains(t, err.Error(), `"n"`)

	err = eval(t, `import "host"; import "other"; fun f(s: other\Site) > void {}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `undefined type "other\Site": other declares no type Site`)

	err = eval(t, `fun f(s: host\Site) > void {}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `undefined type "host\Site"`)
}

func TestCheck_TypeFromAnEarlierChunk(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer func() { _ = sess.Close() }()
	require.NoError(t, sess.Exec(ctx, `object Foo { n: int = 0 }`))
	require.NoError(t, sess.Exec(ctx, `fun f(x: Foo) > int { return x.n; }`))
}

func TestCheck_QualifiedTypeNames(t *testing.T) {
	assert.Nil(t, qualifiedTypeNames(`[Foo]`))
	assert.Equal(t, map[string]string{"Site": "host", "C": `a\b`}, qualifiedTypeNames(`{str:host\Site}|fun(x:a\b\C)>int`))
	assert.Equal(t, []string{"C", "D"}, funTypeParams(`fun::<C,D>(c:C)>D`))
	assert.False(t, namesAType(`{str:[mut [int]]}`))
	assert.True(t, namesAType(`[Foo]`))
}
