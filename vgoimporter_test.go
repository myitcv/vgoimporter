package vgoimporter_test

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"myitcv.io/vgoimporter"
)

const (
	testPkg     = "myitcv.io/vgoimporter/_example"
	testPkgName = "hello"
)

func TestPrePostGoInstall(t *testing.T) {
	if !t.Run("pre", basicTest) {
		return
	}

	cmd := exec.Command("vgo", "test", "myitcv.io/vgoimporter/_example")
	cmd.Dir = filepath.Join(".", "_example")
	want := `# myitcv.io/vgoimporter/_example
./example.go:10:13: undefined: Test
./example.go:19:27: undefined: asdf
`
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if got := string(out); got != want {
		t.Fatalf("unexpected output; got\n%v\nwanted:\n%v", got, want)
	}
	t.Run("post", basicTest)
}

func basicTest(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get absolute working dir: %v", err)
	}

	egDir := filepath.Join(wd, path.Base(testPkg))

	// cmd := exec.Command("vgo", "list", "-json", ".")
	// cmd.Dir = egDir

	// out, err := cmd.CombinedOutput()
	// if err != nil {
	// 	t.Fatalf("failed to run %v: %v\n%s", strings.Join(cmd.Args, " "), err, out)
	// }

	// var p pkgInfo

	// if err := json.Unmarshal(out, &p); err != nil {
	// 	t.Fatalf("failed to json.Unmarshal: %v\n%s", err, out)
	// }

	fset := token.NewFileSet()

	isGo := func(fi os.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), ".go")
	}

	pkgs, err := parser.ParseDir(fset, egDir, isGo, 0)
	if err != nil {
		fatalf("failed to parse %v in dir %v: %v", testPkg, egDir, err)
	}

	pkg, ok := pkgs[testPkgName]
	if !ok {
		fatalf("failed to find %v in %#v", testPkgName, pkgs)
	}

	var files []*ast.File
	for _, f := range pkg.Files {
		files = append(files, f)
	}

	ni := &constFinder{n: "Name"}
	ast.Walk(ni, pkg)

	if ni.i == nil {
		t.Fatalf("failed to find Name constant")
	}

	imp, err := vgoimporter.New(&build.Default, fset, egDir)
	if err != nil {
		fatalf("failed to create importer: %v", err)
	}

	info := &types.Info{
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
		Types: make(map[ast.Expr]types.TypeAndValue),
	}

	conf := types.Config{
		Importer: imp,
		Error:    func(err error) {},
	}

	_, err = conf.Check(testPkg, fset, files, info)
	if err != nil {
		if _, ok := err.(types.Error); !ok {
			fatalf("unexpected error type checking: %v", err)
		}
	}

	tn, ok := info.Defs[ni.i]
	if !ok {
		fatalf("failed to find type for %v", ni.i)
	}

	want := types.UntypedString
	if got, ok := tn.Type().(*types.Basic); !ok || got.Kind() != want {
		t.Fatalf("type of %v: got %v, wanted %v", ni.i, got, want)
	}
}

type constFinder struct {
	n string
	i *ast.Ident
}

func (c *constFinder) Visit(n ast.Node) ast.Visitor {
	switch i := n.(type) {
	case *ast.Ident:
		if i.Obj != nil && i.Obj.Kind == ast.Con && i.Name == c.n {
			c.i = i
			return nil
		}
	}
	return c
}

func fatalf(format string, args ...interface{}) {
	panic(fmt.Errorf(format, args...))
}
