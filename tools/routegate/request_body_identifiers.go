package routegate

import (
	"go/ast"
	"go/importer"
	"go/token"
	"go/types"
)

func resolveFileIdentifiers(fileSet *token.FileSet, file *ast.File) *types.Info {
	var info types.Info

	info.Defs = make(map[*ast.Ident]types.Object)
	info.Uses = make(map[*ast.Ident]types.Object)
	info.Types = make(map[ast.Expr]types.TypeAndValue)

	var configuration types.Config

	configuration.Importer = importer.Default()
	configuration.Error = func(error) {}

	_, _ = configuration.Check(file.Name.Name, fileSet, []*ast.File{file}, &info)

	return &info
}

func identifierType(info *types.Info, identifier *ast.Ident) types.Object {
	if identifier == nil || info == nil {
		return nil
	}

	if object := info.Defs[identifier]; object != nil {
		return object
	}

	return info.Uses[identifier]
}
