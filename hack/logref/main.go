/*
Copyright 2026 Deutsche Telekom AG.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Command logref generates the message catalogue and the field table in
// docs/logging-reference.md from the log calls in the Go code of this module.
//
//	go run ./hack/logref          # rewrite the generated section
//	go run ./hack/logref -check   # fail if the section is out of date
//
// It parses the source without type information, the same way as hack/logref
// in telekom/das-schiff-network-operator. A call is a log call when its
// receiver expression contains "log" and it is a logr Info (with an optional
// V(n)) or Error call. The fields are the literal keys of the key/value pairs
// of the call and of WithValues calls anywhere in the code.
//
// The scan fails when
//   - a log call has no literal message, or a non-literal V level,
//   - a key of a log call or of WithValues is not a string literal, or the
//     key/value list is expanded from a slice or has an odd length,
//   - a field key has no entry in fieldClasses, or an entry for this module's
//     code is no longer used,
//   - a file other than main.go imports the standard library "log" package,
//     whose lines bypass the logger (main.go only discards them).
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const (
	docPath   = "docs/logging-reference.md"
	beginMark = "<!-- BEGIN GENERATED: go run ./hack/logref -->"
	endMark   = "<!-- END GENERATED -->"
	docMode   = 0o600

	controllerRuntime = "controller-runtime"
	// mainFile is the binary's main package, the only file allowed to import the standard library "log".
	mainFile = "main.go"
)

// fieldClass is the personal-data classification of a field, with the classes
// and data subjects of the T-CaaS logging contract.
type fieldClass struct {
	personalData string // none | staff-id | tenant-pii | customer-data | content
	dataSubject  string // none | operator | tenant-user | end-customer
	setBy        string // the library that also adds the field; "" if only this module's code does
	meaning      string
}

// fieldClasses classifies every field a line of this controller can carry.
// Names of objects and hosts chosen by tenants are classified as personal data
// of the tenant user, because they can contain a person's or a customer's name.
var fieldClasses = map[string]fieldClass{
	"IPAddressClaim":   {"tenant-pii", "tenant-user", controllerRuntime, "name of the IPAddressClaim; from controller-runtime also `{name, namespace}` of the reconciled claim"},
	"InfobloxIPPool":   {"tenant-pii", "tenant-user", controllerRuntime, "`{name, namespace}` of the reconciled or validated InfobloxIPPool"},
	"InfobloxInstance": {"none", "none", controllerRuntime, "`{name, namespace}` of the reconciled InfobloxInstance, an object of the operator"},
	"address":          {"customer-data", "tenant-user", "ipamutil", "IP address allocated to a machine of a tenant cluster; from ipamutil the name of the IPAddress object"},
	"claims":           {"none", "none", "", "number of IPAddressClaims that still reference a pool"},
	"controller":       {"none", "none", controllerRuntime, "name of a controller"},
	"controllerGroup":  {"none", "none", controllerRuntime, "API group of the reconciled kind"},
	"controllerKind":   {"none", "none", controllerRuntime, "reconciled kind"},
	"dnsView":          {"none", "none", "", "Infoblox DNS view, set by the operator"},
	"error":            {"customer-data", "tenant-user", "zapr", "error text of an Error line; can repeat a host name, an address or an Infoblox response"},
	"errorCauses":      {"customer-data", "tenant-user", "zap", "the errors of an aggregated error, as `error`"},
	"errorVerbose":     {"customer-data", "tenant-user", "zap", "`error` with a stack trace, for errors that format one"},
	"Pool":             {"tenant-pii", "tenant-user", "ipamutil", "name of the pool of a paused IPAddressClaim"},
	"hostname":         {"customer-data", "tenant-user", "", "host name of the Infoblox host record: the Machine name, with the DNS zone of the pool"},
	"instance":         {"none", "none", "", "name of the InfobloxInstance, an object of the operator"},
	"name":             {"tenant-pii", "tenant-user", controllerRuntime, "name of the reconciled object or of an InfobloxIPPool"},
	"namespace":        {"tenant-pii", "tenant-user", controllerRuntime, "namespace of the reconciled object or of an InfobloxIPPool"},
	"networkView":      {"none", "none", "", "Infoblox network view, set by the operator"},
	"reconcileID":      {"none", "none", controllerRuntime, "ID of one reconciliation"},
	"requestID":        {"none", "none", controllerRuntime, "UID of an admission request (`--zap-log-level=5` and higher)"},
	"resource":         {"none", "none", controllerRuntime, "resource of an admission request (`--zap-log-level=5` and higher)"},
	"subnet":           {"customer-data", "tenant-user", "", "subnet of an InfobloxIPPool that addresses are allocated from"},
	"user":             {"tenant-pii", "tenant-user", controllerRuntime, "user name of an admission request (`--zap-log-level=5` and higher)"},
	"value":            {"none", "none", "", "invalid value of a flag"},
	"version":          {"none", "none", "", "version of the controller"},
	"webhook":          {"none", "none", "", "name of a webhook"},
	"webhookGroup":     {"none", "none", controllerRuntime, "API group of the validated kind"},
	"webhookKind":      {"none", "none", controllerRuntime, "validated kind"},
}

var (
	scanPaths = []string{mainFile, "api", "internal", "pkg"}
	// levelOrder is the order of the levels in the catalogue; V(2) and higher follow.
	levelOrder = []string{"error", "info", "debug"}
)

type entry struct {
	level, message string
	fields         map[string]bool
}

type scanResult struct {
	pkgs      map[string]map[string]*entry // package dir -> level + "\x00" + message -> entry
	withKeys  map[string]bool              // keys of WithValues calls
	callKeys  map[string]bool              // keys of log calls
	usedKeys  map[string]bool              // keys of log calls and WithValues calls
	stdlibLog []string
	errs      []error
	fset      *token.FileSet
	root      string
}

func main() {
	check := flag.Bool("check", false, "fail if "+docPath+" is out of date instead of rewriting it")
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := run(*root, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root string, check bool) error {
	res, err := scan(root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, docPath)
	doc, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("read %s: %w", docPath, err)
	}
	begin := bytes.Index(doc, []byte(beginMark))
	end := bytes.Index(doc, []byte(endMark))
	if begin < 0 || end < begin {
		return fmt.Errorf("%s needs the markers %q and %q", docPath, beginMark, endMark)
	}
	updated := string(doc[:begin+len(beginMark)]) + "\n" + render(res) + string(doc[end:])
	if updated == string(doc) {
		return nil
	}
	if check {
		return fmt.Errorf("%s is out of date with the log calls in the code; run `go run ./hack/logref` and commit the result", docPath)
	}
	if err := os.WriteFile(path, []byte(updated), docMode); err != nil {
		return fmt.Errorf("write %s: %w", docPath, err)
	}
	return nil
}

func scan(root string) (*scanResult, error) {
	s := &scanResult{
		pkgs:     map[string]map[string]*entry{},
		withKeys: map[string]bool{},
		callKeys: map[string]bool{},
		usedKeys: map[string]bool{},
		fset:     token.NewFileSet(),
		root:     root,
	}
	for _, top := range scanPaths {
		if err := filepath.WalkDir(filepath.Join(root, top), s.visit); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("scan %s: %w", top, err)
		}
	}
	if len(s.stdlibLog) > 0 {
		s.errs = append(s.errs, fmt.Errorf("these files import the standard library \"log\" package, whose output ignores --log-level and --log-format; use a logr logger (log.FromContext(ctx)) instead: %s", strings.Join(s.stdlibLog, ", ")))
	}
	for _, k := range sortedKeys(s.usedKeys) {
		if _, ok := fieldClasses[k]; !ok {
			s.errs = append(s.errs, fmt.Errorf("field %q has no personal-data classification; add it to fieldClasses in hack/logref/main.go", k))
		}
	}
	for _, k := range sortedKeys(fieldClasses) {
		if fieldClasses[k].setBy == "" && !s.usedKeys[k] {
			s.errs = append(s.errs, fmt.Errorf("field %q is classified as a field of this module's code, but no log call uses it; remove it from fieldClasses", k))
		}
	}
	if err := errors.Join(s.errs...); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *scanResult) visit(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(s.root, path)
	rel = filepath.ToSlash(rel)
	if d.IsDir() {
		if d.Name() == "testdata" {
			return filepath.SkipDir
		}
		return nil
	}
	if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(d.Name(), "zz_generated") {
		return nil
	}
	f, err := parser.ParseFile(s.fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("parse %s: %w", rel, err)
	}
	for _, imp := range f.Imports {
		if ip, _ := strconv.Unquote(imp.Path.Value); ip == "log" && rel != mainFile {
			s.stdlibLog = append(s.stdlibLog, rel)
		}
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	if dir == "." {
		dir = mainFile
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if err := s.addCall(dir, call); err != nil {
			s.errs = append(s.errs, fmt.Errorf("%s: %w", s.fset.Position(call.Pos()), err))
		}
		return true
	})
	return nil
}

// addCall records call if it is a log call or a WithValues call. It returns an
// error for a log call whose message, V level or keys are not literals.
func (s *scanResult) addCall(dir string, call *ast.CallExpr) error {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	if sel.Sel.Name == "WithValues" {
		ks, err := keys(call, call.Args)
		if err != nil {
			return fmt.Errorf("%s.WithValues: %w", types.ExprString(sel.X), err)
		}
		for _, k := range ks {
			s.withKeys[k], s.usedKeys[k] = true, true
		}
		return nil
	}
	recv := types.ExprString(sel.X)
	method := sel.Sel.Name
	if !strings.Contains(strings.ToLower(recv), "log") || (method != "Info" && method != "Error") {
		return nil
	}
	var level string
	var msgArg ast.Expr
	var kv []ast.Expr
	switch {
	case method == "Error" && len(call.Args) >= 2:
		level, msgArg, kv = "error", call.Args[1], call.Args[2:]
	case method == "Info" && len(call.Args) >= 1:
		var err error
		if level, err = infoLevel(sel.X); err != nil {
			return fmt.Errorf("log call %s.%s: %w", recv, method, err)
		}
		msgArg, kv = call.Args[0], call.Args[1:]
	default:
		return fmt.Errorf("log call %s.%s has too few arguments", recv, method)
	}
	msg := literal(msgArg)
	if msg == "" {
		return fmt.Errorf("log call %s.%s has a non-literal message %s; use a literal message and key/value fields", recv, method, types.ExprString(msgArg))
	}
	ks, err := keys(call, kv)
	if err != nil {
		return fmt.Errorf("log call %s.%s: %w", recv, method, err)
	}
	if s.pkgs[dir] == nil {
		s.pkgs[dir] = map[string]*entry{}
	}
	key := level + "\x00" + msg
	e := s.pkgs[dir][key]
	if e == nil {
		e = &entry{level: level, message: msg, fields: map[string]bool{}}
		s.pkgs[dir][key] = e
	}
	for _, k := range ks {
		e.fields[k], s.callKeys[k], s.usedKeys[k] = true, true, true
	}
	return nil
}

// infoLevel returns the catalogue level of a logr Info call on recv. It adds
// up the V calls of the receiver chain, as logr does.
func infoLevel(recv ast.Expr) (string, error) {
	v := 0
	for x := recv; ; {
		switch e := x.(type) {
		case *ast.CallExpr:
			sel, ok := e.Fun.(*ast.SelectorExpr)
			if !ok {
				return levelName(v), nil
			}
			if sel.Sel.Name == "V" {
				n, err := intLiteral(e.Args)
				if err != nil {
					return "", err
				}
				v += n
			}
			x = sel.X
		case *ast.SelectorExpr:
			x = e.X
		default:
			return levelName(v), nil
		}
	}
}

func intLiteral(args []ast.Expr) (int, error) {
	if len(args) == 1 {
		if lit, ok := args[0].(*ast.BasicLit); ok && lit.Kind == token.INT {
			return strconv.Atoi(lit.Value)
		}
	}
	return 0, errors.New("non-literal V level")
}

func levelName(v int) string {
	switch v {
	case 0:
		return "info"
	case 1:
		return "debug"
	default:
		return fmt.Sprintf("V(%d)", v)
	}
}

// keys returns the keys of the key/value list kv of call. Every key must be a
// string literal, so that no field escapes the classification.
func keys(call *ast.CallExpr, kv []ast.Expr) ([]string, error) {
	if call.Ellipsis.IsValid() {
		return nil, errors.New("expanded key/value list; pass literal keys")
	}
	if len(kv)%2 != 0 {
		return nil, errors.New("odd number of key/value arguments")
	}
	out := make([]string, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k := literal(kv[i])
		if k == "" {
			return nil, fmt.Errorf("non-literal key %s; use a string literal", types.ExprString(kv[i]))
		}
		out = append(out, k)
	}
	return out, nil
}

func literal(x ast.Expr) string {
	if lit, ok := x.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if s, err := strconv.Unquote(lit.Value); err == nil {
			return s
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func code(s string) string {
	return "`" + strings.ReplaceAll(strings.ReplaceAll(s, "`", "'"), "|", `\|`) + "`"
}

func sortedEntries(es map[string]*entry) []*entry {
	out := make([]*entry, 0, len(es))
	for _, k := range sortedKeys(es) {
		out = append(out, es[k])
	}
	rank := func(level string) int {
		if i := slices.Index(levelOrder, level); i >= 0 {
			return i
		}
		return len(levelOrder)
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].level) < rank(out[j].level) })
	return out
}

func render(s *scanResult) string {
	var b strings.Builder
	b.WriteString("\n### Messages\n\n")
	b.WriteString("The literal message of each log call of this module, by package. `debug` lines are logged at logr verbosity 1.\n")
	for _, dir := range sortedKeys(s.pkgs) {
		fmt.Fprintf(&b, "\n#### `%s`\n\n| Level | Message | Fields |\n|---|---|---|\n", dir)
		for _, e := range sortedEntries(s.pkgs[dir]) {
			fields := make([]string, 0, len(e.fields))
			for _, f := range sortedKeys(e.fields) {
				fields = append(fields, code(f))
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", e.level, code(e.message), strings.Join(fields, ", "))
		}
	}
	b.WriteString("\n### Fields\n\n")
	b.WriteString("The fields of the lines of this module, and the fields that libraries add to them. \"Set by\" names where the field comes from: a log call of this module, a `WithValues` call of this module (the field is on every later line of that logger), or a library.\n\n")
	b.WriteString("| Field | Set by | Personal data | Data subject | Meaning |\n|---|---|---|---|---|\n")
	for _, k := range sortedKeys(fieldClasses) {
		c := fieldClasses[k]
		var by []string
		if s.callKeys[k] {
			by = append(by, "log call")
		}
		if s.withKeys[k] {
			by = append(by, "`WithValues`")
		}
		if c.setBy != "" {
			by = append(by, c.setBy)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", code(k), strings.Join(by, ", "), c.personalData, c.dataSubject, c.meaning)
	}
	b.WriteString("\n")
	return b.String()
}
