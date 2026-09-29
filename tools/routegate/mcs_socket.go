package routegate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	mcsExpectedCallsite       = "third_party/go-push-receiver/fcm.go:tryToConnect"
	mcsLengthEncodingVarint   = "protobuf-varint"
	mcsProtocolImportPath     = "github.com/portpowered/go-ring/internal/protocol"
	mcsPBImportPath           = "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/mcs"
	mcsTLSImportPath          = "crypto/tls"
	mcsInjectedDialMethodName = "mcsDialContext"
	mcsVarintSizePacketLenMax = 5
)

type mcsInventory struct {
	//nolint:tagliatelle // Keys mirror the checked-in snake_case inventory schema.
	Socket   mcsSocketInventory   `yaml:"mcs_socket"`
	Protobuf mcsProtobufInventory `yaml:"protobuf"`
}

type mcsSocketInventory struct {
	Host           string         `yaml:"host"`
	Port           int            `yaml:"port"`
	Network        string         `yaml:"network"`
	TLS            bool           `yaml:"tls"`
	Domain         string         `yaml:"domain"`
	Version        int            `yaml:"version"`
	VersionBytes   int            `yaml:"version_bytes"`   //nolint:tagliatelle // External schema key.
	TagBytes       int            `yaml:"tag_bytes"`       //nolint:tagliatelle // External schema key.
	LengthEncoding string         `yaml:"length_encoding"` //nolint:tagliatelle // External schema key.
	Callsite       string         `yaml:"callsite"`
	Tags           map[string]int `yaml:"tags"`
}

type mcsProtobufInventory struct {
	SourceFiles []mcsInventoryFile `yaml:"source_files"` //nolint:tagliatelle // External schema key.
}

type mcsInventoryFile struct {
	Path     string   `yaml:"path"`
	Messages []string `yaml:"messages"`
}

func expectedMCSTags() map[string]int {
	return map[string]int{
		"HeartbeatPing":       0,
		"HeartbeatAck":        1,
		"LoginRequest":        2,
		"LoginResponse":       3,
		"Close":               4,
		"MessageStanza":       5,
		"PresenceStanza":      6,
		"IqStanza":            7,
		"DataMessageStanza":   8,
		"BatchPresenceStanza": 9,
		"StreamErrorStanza":   10,
		"HTTPRequest":         11,
		"HTTPResponse":        12,
		"BindAccountRequest":  13,
		"BindAccountResponse": 14,
		"TalkMetadata":        15,
		"NumProtoTypes":       16,
		"Unknown":             255,
	}
}

func expectedMCSActiveMessages() map[string]string {
	return map[string]string{
		"HeartbeatPing":     "HeartbeatPing",
		"HeartbeatAck":      "HeartbeatAck",
		"LoginRequest":      "LoginRequest",
		"LoginResponse":     "LoginResponse",
		"Close":             "Close",
		"IqStanza":          "IqStanza",
		"DataMessageStanza": "DataMessageStanza",
		"StreamErrorStanza": "StreamErrorStanza",
	}
}

func auditMCSSocket(root string, parsed []*parsedGoFile) (map[*ast.CallExpr]bool, []Finding) {
	sources := parsedSourcesByRelativePath(root, parsed)
	if sources["third_party/go-push-receiver/fcm.go"] == nil {
		return nil, nil
	}

	verified := make(map[*ast.CallExpr]bool)

	var findings []Finding

	add := func(path, rule, message string) {
		findings = append(findings, Finding{Path: path, Line: 0, Rule: rule, Message: message})
	}

	inventory, inventoryValid := loadMCSInventory(root)
	if !inventoryValid {
		add("api/external/push-protocol-inventory.yaml", "invalid-mcs-socket-contract",
			"MCS socket inventory must pin the host, port, network, TLS, framing, callsite, and complete tag map")
	}

	if !verifyMCSGeneratedConstants(root, inventory.Socket) {
		add("internal/protocol/mcs.gen.go", "unbound-mcs-protocol-constants",
			"generated MCS constants and tag values must match the socket inventory")
	}

	if !verifyMCSReceiverConstants(sources) {
		add("third_party/go-push-receiver/constants.go", "unbound-mcs-protocol-constants",
			"MCS domain, version, and packet framing constants must alias generated protocol values")
	}

	if !verifyMCSTagAliases(sources) || !verifyMCSTagStrings(sources) ||
		!verifyMCSGenerateMessage(sources, inventory.Protobuf.SourceFiles) {
		add("third_party/go-push-receiver/tag.go", "unbound-mcs-tag-mapping",
			"MCS tag aliases, display names, and active protobuf message mappings must match the inventory")
	}

	dialCall, dialValid := verifyMCSDialCallsite(sources, inventory.Socket)
	if !dialValid {
		add("third_party/go-push-receiver/fcm.go", "unbound-mcs-dial-callsite",
			"MCS dial paths must use the inventoried TLS callsite and generated network/address constants")
	} else if inventoryValid {
		verified[dialCall] = true
	}

	if !verifyMCSDependencyDialInventory(parsed, dialCall, dialValid) {
		add("third_party/go-push-receiver", "unlisted-mcs-network-dial",
			"the pinned receiver contains an additional or unbound raw MCS network dial")
	}

	return verified, findings
}

func loadMCSInventory(root string) (mcsInventory, bool) {
	var inventory mcsInventory

	path := filepath.ToSlash(filepath.Join("api", "external", "push-protocol-inventory.yaml"))

	raw, err := fs.ReadFile(os.DirFS(root), path)
	if err != nil {
		return inventory, false
	}

	if !mcsInventorySocketKeysMatch(raw) {
		return inventory, false
	}

	if yaml.Unmarshal(raw, &inventory) != nil {
		return inventory, false
	}

	socket := inventory.Socket
	if socket.Host != "mtalk.google.com" || socket.Port != 5228 || socket.Network != "tcp" || !socket.TLS ||
		socket.Domain != "mcs.android.com" || socket.Version != 41 || socket.VersionBytes != 1 ||
		socket.TagBytes != 1 || socket.LengthEncoding != mcsLengthEncodingVarint ||
		socket.Callsite != mcsExpectedCallsite || !sameMCSTags(socket.Tags) {
		return inventory, false
	}

	messages := mcsSourceMessages(inventory.Protobuf.SourceFiles)
	if !sameMCSStringSet(messages, expectedActiveMessageNames()) {
		return inventory, false
	}

	return inventory, true
}

func mcsInventorySocketKeysMatch(raw []byte) bool {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil || document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return false
	}

	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return false
	}

	var socket *yaml.Node

	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value == "mcs_socket" {
			socket = root.Content[index+1]
		}
	}

	if socket == nil || socket.Kind != yaml.MappingNode {
		return false
	}

	wanted := map[string]bool{
		"host": true, "port": true, "network": true, "tls": true, "domain": true,
		"version": true, "version_bytes": true, "tag_bytes": true,
		"length_encoding": true, "callsite": true, "tags": true,
	}
	if len(socket.Content) != 2*len(wanted) {
		return false
	}

	for index := 0; index+1 < len(socket.Content); index += 2 {
		key := socket.Content[index].Value
		if !wanted[key] {
			return false
		}

		delete(wanted, key)
	}

	return len(wanted) == 0
}

func sameMCSTags(actual map[string]int) bool {
	wanted := expectedMCSTags()
	if len(actual) != len(wanted) {
		return false
	}

	for name, value := range wanted {
		if actual[name] != value {
			return false
		}
	}

	return true
}

func mcsSourceMessages(files []mcsInventoryFile) []string {
	for _, file := range files {
		if file.Path == "third_party/go-push-receiver/proto/mcs.proto" {
			return file.Messages
		}
	}

	return nil
}

func expectedActiveMessageNames() []string {
	wanted := expectedMCSActiveMessages()

	names := make([]string, 0, len(wanted))

	for _, message := range wanted {
		names = append(names, message)
	}

	return names
}

func sameMCSStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	values := make(map[string]bool, len(left))
	for _, value := range left {
		if values[value] {
			return false
		}

		values[value] = true
	}

	for _, value := range right {
		if !values[value] {
			return false
		}
	}

	return true
}

func verifyMCSGeneratedConstants(root string, socket mcsSocketInventory) bool {
	path := filepath.Join(root, "internal", "protocol", "mcs.gen.go")

	constants, ok := goConstants(path)
	if !ok {
		return false
	}

	stringsExpected := map[string]string{
		"MCSHost":           socket.Host,
		"MCSPort":           strconv.Itoa(socket.Port),
		"MCSAddress":        socket.Host + ":" + strconv.Itoa(socket.Port),
		"MCSNetwork":        socket.Network,
		"MCSDomain":         socket.Domain,
		"MCSLengthEncoding": socket.LengthEncoding,
	}
	for name, expected := range stringsExpected {
		if !mcsStringConstantEquals(constants[name], expected) {
			return false
		}
	}

	integersExpected := map[string]int{
		"MCSVersion":            socket.Version,
		"MCSVersionPacketBytes": socket.VersionBytes,
		"MCSTagPacketBytes":     socket.TagBytes,
	}
	for name, expected := range integersExpected {
		if !mcsIntConstantEquals(constants[name], expected) {
			return false
		}
	}

	if !mcsBoolConstantEquals(constants["MCSTLS"], socket.TLS) {
		return false
	}

	for tag, value := range socket.Tags {
		if !mcsIntConstantEquals(constants["MCS"+tag+"Tag"], value) {
			return false
		}
	}

	generatedTags := 0

	for name := range constants {
		if strings.HasPrefix(name, "MCS") && strings.HasSuffix(name, "Tag") {
			generatedTags++
		}
	}

	return generatedTags == len(socket.Tags)
}

func goConstants(path string) (map[string]ast.Expr, bool) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, false
	}

	values := make(map[string]ast.Expr)

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}

		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || len(value.Values) != 1 {
				continue
			}

			for _, name := range value.Names {
				values[name.Name] = value.Values[0]
			}
		}
	}

	return values, true
}

func mcsStringConstantEquals(expression ast.Expr, expected string) bool {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return false
	}

	value, err := strconv.Unquote(literal.Value)

	return err == nil && value == expected
}

func mcsIntConstantEquals(expression ast.Expr, expected int) bool {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return false
	}

	value, err := strconv.Atoi(literal.Value)

	return err == nil && value == expected
}

func mcsBoolConstantEquals(expression ast.Expr, expected bool) bool {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return false
	}

	return identifier.Name == strconv.FormatBool(expected)
}

func verifyMCSReceiverConstants(sources map[string]*parsedGoFile) bool {
	file := sources["third_party/go-push-receiver/constants.go"]
	if file == nil || importAliases(file.file)["protocol"] != mcsProtocolImportPath {
		return false
	}

	constants := constantsFromFile(file.file)

	aliases := map[string]string{
		"mcsDomain":        "MCSDomain",
		"fcmVersion":       "MCSVersion",
		"versionPacketLen": "MCSVersionPacketBytes",
		"tagPacketLen":     "MCSTagPacketBytes",
	}

	for name, protocolName := range aliases {
		if !mcsProtocolAlias(constants[name], protocolName) {
			return false
		}
	}

	return mcsIntConstantEquals(constants["sizePacketLenMin"], 1) &&
		mcsIntConstantEquals(constants["sizePacketLenMax"], mcsVarintSizePacketLenMax)
}

func constantsFromFile(file *ast.File) map[string]ast.Expr {
	values := make(map[string]ast.Expr)

	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}

		for _, specification := range general.Specs {
			value, ok := specification.(*ast.ValueSpec)
			if !ok || len(value.Values) != 1 {
				continue
			}

			for _, name := range value.Names {
				values[name.Name] = value.Values[0]
			}
		}
	}

	return values
}

func mcsProtocolAlias(expression ast.Expr, name string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}

	qualifier, ok := selector.X.(*ast.Ident)

	return ok && qualifier.Name == "protocol"
}

func parsedPathIs(file *parsedGoFile, suffix string) bool {
	path := filepath.ToSlash(file.path)

	return path == suffix || strings.HasSuffix(path, "/"+suffix)
}

func parsedPathUnder(file *parsedGoFile, directory string) bool {
	path := filepath.ToSlash(file.path)

	return strings.HasPrefix(path, directory+"/") || strings.Contains(path, "/"+directory+"/")
}

func isMCSNetworkDialName(name string) bool {
	switch name {
	case "Dial", "DialContext", "DialTimeout", "DialWithDialer", mcsInjectedDialMethodName:
		return true
	default:
		return false
	}
}
