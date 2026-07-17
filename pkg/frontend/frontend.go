// Package frontend loads a Go package with go/packages, type-checks it with
// go/types, and hands the typed AST and its type information to the IR
// builder. hebi is type-directed from the first line, so the type facts
// gathered here drive every later lowering decision.
package frontend

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Package is a loaded, type-checked Go package: the syntax hebi will lower, the
// type information go/types produced, and the fileset that positions map back
// to. Info is the spine every later stage reads.
type Package struct {
	// PkgPath is the import path, or command-line-arguments for a package
	// loaded from a bare file.
	PkgPath string
	// Name is the package clause name, such as main.
	Name string
	// Fset owns the positions of every node in Files.
	Fset *token.FileSet
	// Files are the parsed source files in a stable order.
	Files []*ast.File
	// Types is the type-checked package.
	Types *types.Package
	// Info carries what go/types resolved: every expression's type, every
	// identifier's object, and the rest.
	Info *types.Info
}

// loadMode asks go/packages for exactly what the frontend needs: names, the
// files, the parsed syntax, the checked types and type info, and the import
// graph so imported types resolve.
const loadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedImports |
	packages.NeedDeps |
	packages.NeedTypes |
	packages.NeedSyntax |
	packages.NeedTypesInfo

// Load loads and type-checks the Go package at path, which may be a directory
// or a single .go file. It fails if go/packages reports any error, if the
// pattern does not resolve to exactly one package, or if type checking finds a
// problem anywhere in the package or its dependencies, because hebi will not
// lower a package it cannot fully type.
func Load(path string) (*Package, error) {
	pattern, dir, err := patternFor(path)
	if err != nil {
		return nil, err
	}
	cfg := &packages.Config{
		Mode:  loadMode,
		Dir:   dir,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, pattern)
	if err != nil {
		return nil, fmt.Errorf("frontend: load %s: %w", path, err)
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("frontend: %s resolved to %d packages, want exactly one", path, len(pkgs))
	}
	pkg := pkgs[0]
	if err := firstError(pkg); err != nil {
		return nil, err
	}
	// go/packages fills these together, so a nil here means a partial load;
	// guard so a later stage never dereferences a nil Info.
	if pkg.Types == nil || pkg.TypesInfo == nil || pkg.Fset == nil {
		return nil, fmt.Errorf("frontend: %s loaded without complete type information", path)
	}
	return &Package{
		PkgPath: pkg.PkgPath,
		Name:    pkg.Name,
		Fset:    pkg.Fset,
		Files:   pkg.Syntax,
		Types:   pkg.Types,
		Info:    pkg.TypesInfo,
	}, nil
}

// patternFor turns a path into a go/packages pattern and the directory the load
// runs in. A .go file becomes a file= query in its own directory, so the module
// context around it is honored; a directory loads as the package in that
// directory.
func patternFor(path string) (pattern, dir string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("frontend: %w", err)
	}
	if info.IsDir() {
		return ".", path, nil
	}
	if !strings.HasSuffix(path, ".go") {
		return "", "", fmt.Errorf("frontend: %s is not a .go file or a directory", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("frontend: %w", err)
	}
	return "file=" + abs, filepath.Dir(abs), nil
}

// firstError returns the first error go/packages attached to the package or any
// of its dependencies, in a stable order so the reported message is a pure
// function of the input.
func firstError(root *packages.Package) error {
	type pkgError struct {
		path string
		err  packages.Error
	}
	var all []pkgError
	packages.Visit([]*packages.Package{root}, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			all = append(all, pkgError{p.PkgPath, e})
		}
	})
	if len(all) == 0 {
		return nil
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].path != all[j].path {
			return all[i].path < all[j].path
		}
		return all[i].err.Pos < all[j].err.Pos
	})
	return fmt.Errorf("frontend: %s: %s", all[0].path, all[0].err)
}
