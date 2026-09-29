// The route gate uses parser object identity for lexical scope checks in untyped source.

package routegate

import "go/ast"

func exactObject(expression ast.Expr, object *ast.Object) bool {
	identifier, ok := expression.(*ast.Ident)

	return ok && identifier.Obj == object
}

func containsObject(node ast.Node, object *ast.Object) bool {
	contains := false

	ast.Inspect(node, func(child ast.Node) bool {
		identifier, ok := child.(*ast.Ident)
		if ok && identifier.Obj == object {
			contains = true

			return false
		}

		return !contains
	})

	return contains
}

func requestRoot(node ast.Node) *ast.Object {
	expression, ok := node.(ast.Expr)
	if !ok {
		return nil
	}

	identifier := rootIdentifierFromExpression(expression)
	if identifier == nil {
		return nil
	}

	return identifier.Obj
}

func rootIdentifierFromExpression(expression ast.Expr) *ast.Ident {
	for {
		switch value := expression.(type) {
		case *ast.Ident:
			return value
		case *ast.SelectorExpr:
			expression = value.X
		case *ast.ParenExpr:
			expression = value.X
		case *ast.IndexExpr:
			expression = value.X
		case *ast.IndexListExpr:
			expression = value.X
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok {
				return nil
			}

			expression = selector.X
		default:
			return nil
		}
	}
}

func selectorChain(expression ast.Expr) []string {
	var names []string

	for {
		selector, ok := expression.(*ast.SelectorExpr)
		if !ok {
			break
		}

		names = append([]string{selector.Sel.Name}, names...)
		expression = selector.X
	}

	return names
}
