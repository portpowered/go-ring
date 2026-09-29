package routegate

import (
	"go/ast"
	"strconv"
	"strings"
)

func verifyMCSTagAliases(sources map[string]*parsedGoFile) bool {
	file := sources["third_party/go-push-receiver/tag.go"]
	if file == nil || importAliases(file.file)["protocol"] != mcsProtocolImportPath {
		return false
	}

	constants := constantsFromFile(file.file)

	wantedTags := expectedMCSTags()

	if len(constants) < len(wantedTags) {
		return false
	}

	localTagConstants := 0

	for name := range constants {
		if strings.HasPrefix(name, "tag") {
			localTagConstants++

			if _, exists := wantedTags[strings.TrimPrefix(name, "tag")]; !exists {
				return false
			}
		}
	}

	if localTagConstants != len(wantedTags) {
		return false
	}

	for tag := range wantedTags {
		if !mcsProtocolAlias(constants["tag"+tag], "MCS"+tag+"Tag") {
			return false
		}
	}

	return true
}

func verifyMCSTagStrings(sources map[string]*parsedGoFile) bool {
	file := sources["third_party/go-push-receiver/tag.go"]
	if file == nil {
		return false
	}

	function := mcsMethod(file.file, "String")
	if function == nil {
		return false
	}

	switchNode := firstSwitch(function.Body)
	if switchNode == nil || !isIdent(switchNode.Tag, "t") {
		return false
	}

	wantedTags := expectedMCSTags()
	seen := make(map[string]bool)

	for _, statement := range switchNode.Body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok || clause.List == nil {
			continue
		}

		for _, expression := range clause.List {
			identifier, ok := expression.(*ast.Ident)
			if !ok || !strings.HasPrefix(identifier.Name, "tag") {
				return false
			}

			tag := strings.TrimPrefix(identifier.Name, "tag")

			value, exists := wantedTags[tag]

			if !exists || seen[tag] || len(clause.Body) != 1 {
				return false
			}

			returned, ok := clause.Body[0].(*ast.ReturnStmt)
			if !ok || len(returned.Results) != 1 {
				return false
			}

			if tag == "Unknown" {
				if !mcsUnknownTagString(returned.Results[0]) {
					return false
				}
			} else if !mcsStringConstantEquals(returned.Results[0],
				tag+"("+strconv.Itoa(value)+")") {
				return false
			}

			seen[tag] = true
		}
	}

	return len(seen) == len(wantedTags)
}

func mcsUnknownTagString(expression ast.Expr) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 || !mcsStringConstantEquals(call.Args[0], "Unknown(%d)") ||
		!isIdent(call.Args[1], "t") {
		return false
	}

	selector, ok := call.Fun.(*ast.SelectorExpr)

	return ok && isIdent(selector.X, "fmt") && selector.Sel.Name == "Sprintf"
}

func verifyMCSGenerateMessage(sources map[string]*parsedGoFile, files []mcsInventoryFile) bool {
	file := sources["third_party/go-push-receiver/tag.go"]
	if file == nil || importAliases(file.file)["pb"] != mcsPBImportPath {
		return false
	}

	function := mcsMethod(file.file, "GenerateMessage")
	if function == nil {
		return false
	}

	switchNode := firstSwitch(function.Body)
	if switchNode == nil || !isIdent(switchNode.Tag, "t") {
		return false
	}

	active, seen, defaultSeen, valid := mcsMessageSwitchMappings(switchNode)
	if !valid {
		return false
	}

	wantedTags := expectedMCSTags()

	return defaultSeen && len(seen) == len(wantedTags) &&
		sameMCSActiveMessages(active) && sameMCSStringSet(mcsSourceMessages(files), expectedActiveMessageNames())
}

func mcsMessageSwitchMappings(switchNode *ast.SwitchStmt) (map[string]string, map[string]bool, bool, bool) {
	active := make(map[string]string)
	seen := make(map[string]bool)
	defaultSeen := false

	for _, statement := range switchNode.Body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok {
			continue
		}

		if clause.List == nil {
			if defaultSeen || len(clause.Body) != 1 || !mcsReturnsNil(clause.Body[0]) {
				return nil, nil, false, false
			}

			defaultSeen = true

			continue
		}

		if !addMCSMessageCase(clause, active, seen) {
			return nil, nil, false, false
		}
	}

	return active, seen, defaultSeen, true
}

func addMCSMessageCase(clause *ast.CaseClause, active map[string]string, seen map[string]bool) bool {
	if len(clause.Body) != 1 {
		return false
	}

	returned, ok := clause.Body[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		return false
	}

	message, isActive, ok := mcsGeneratedMessage(returned.Results[0])
	if !ok {
		return false
	}

	wantedTags := expectedMCSTags()

	for _, expression := range clause.List {
		identifier, ok := expression.(*ast.Ident)
		if !ok || !strings.HasPrefix(identifier.Name, "tag") {
			return false
		}

		tag := strings.TrimPrefix(identifier.Name, "tag")
		if _, exists := wantedTags[tag]; !exists || seen[tag] {
			return false
		}

		seen[tag] = true

		if isActive {
			active[tag] = message
		}
	}

	return !isActive || len(clause.List) == 1
}

func mcsGeneratedMessage(expression ast.Expr) (string, bool, bool) {
	if isIdent(expression, "nil") {
		return "", false, true
	}

	_, ok := expression.(*ast.CallExpr)
	if !ok {
		return "", false, false
	}

	return mcsNewMessageName(expression)
}

func mcsNewMessageName(expression ast.Expr) (string, bool, bool) {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || !isIdent(call.Fun, "new") {
		return "", false, false
	}

	selector, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok || !isIdent(selector.X, "pb") {
		return "", false, false
	}

	return selector.Sel.Name, true, true
}

func mcsReturnsNil(statement ast.Stmt) bool {
	returned, ok := statement.(*ast.ReturnStmt)

	return ok && len(returned.Results) == 1 && isIdent(returned.Results[0], "nil")
}

func sameMCSActiveMessages(actual map[string]string) bool {
	wanted := expectedMCSActiveMessages()
	if len(actual) != len(wanted) {
		return false
	}

	for tag, message := range wanted {
		if actual[tag] != message {
			return false
		}
	}

	return true
}

func mcsMethod(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == name && function.Recv != nil {
			return function
		}
	}

	return nil
}

func firstSwitch(node ast.Node) *ast.SwitchStmt {
	var result *ast.SwitchStmt

	ast.Inspect(node, func(child ast.Node) bool {
		if result != nil {
			return false
		}

		if switchNode, ok := child.(*ast.SwitchStmt); ok {
			result = switchNode

			return false
		}

		return true
	})

	return result
}

func isIdent(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)

	return ok && identifier.Name == name
}
