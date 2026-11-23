package buzzgen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/require"
)

func TestScalarMethod(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOWORK", "off")
	root, err := filepath.Abs("..")
	require.NoError(t, err)
	module := "github.com/egladman/magus/libs/gopherbuzz"
	version, err := os.ReadFile(filepath.Join(root, "go.mod"))
	require.NoError(t, err)
	mod := strings.Replace(string(version), "module "+module, "module example.com/body", 1)
	mod += fmt.Sprintf("\nrequire %s v0.0.0\nreplace %s => %s\n", module, module, root)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0644))
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0644))
	const source = `package main
import "github.com/egladman/magus/libs/gopherbuzz/vm"
func integer(v vm.Value) vm.Value {
	if v.IsInt() { return v }
	return vm.IntValue(0)
}
func main() {
	println(integer(vm.IntValue(17)).AsInt(), integer(vm.False).AsInt())
}
`
	for _, fallback := range []string{"0", "23"} {
		src := strings.Replace(source, "IntValue(0)", "IntValue("+fallback+")", 1)
		require.NoError(t, os.WriteFile(filepath.Join(dir,"main.go"), []byte(src), 0644))
		p, err := Load(dir, Config{}, "example.com/body")
		require.NoError(t,err)
		method, err := p.ScalarMethod("example.com/body", "integer", "integerValue", "int")
		require.NoError(t,err)
		cmd := exec.CommandContext(t.Context(),"go","run",".")
		cmd.Dir = dir
		native, err := cmd.CombinedOutput()
		require.NoError(t,err,"%s",native)
		s := buzz.NewSession(t.Context())
		t.Cleanup(func(){ require.NoError(t,s.Close()) })
		source := "object Wrapped { data: any,\n"+method+"}\nfinal a = Wrapped{data = 17}.integerValue(); final b = Wrapped{data = false}.integerValue();"
		require.NoError(t,s.Exec(t.Context(),source))
		require.Equal(t,string(native),fmt.Sprintf("%s %s\n",s.Globals()["a"].String(),s.Globals()["b"].String()))
		require.Equal(t,fallback,s.Globals()["b"].String())
	}
}
