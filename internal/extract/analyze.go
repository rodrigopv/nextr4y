package extract

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

type Module struct {
	ID                  string
	File                string
	StartByte, EndByte  int
	Requires            []string
	LazyChunkIDs        []string
	UnresolvedLazyCalls int
}
type Analysis struct {
	Status     string
	Modules    []Module
	References []string // Literal URL/path candidates only, never requested as APIs.
	Warnings   []string
}

var digits = regexp.MustCompile(`^[0-9]+$`)

// Walk AST fields only; File and scope metadata are not syntax children.
func walk(node any, visit func(any)) {
	seen := map[uintptr]bool{}
	var walkValue func(reflect.Value, int)
	walkValue = func(v reflect.Value, depth int) {
		if !v.IsValid() || depth > 512 {
			return
		}
		if v.Kind() == reflect.Interface {
			if !v.IsNil() {
				walkValue(v.Elem(), depth+1)
			}
			return
		}
		if v.Kind() == reflect.Pointer {
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if v.Type().Elem().PkgPath() != "github.com/dop251/goja/ast" {
				return
			}
			visit(v.Interface())
			walkValue(v.Elem(), depth+1)
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).CanInterface() {
					walkValue(v.Field(i), depth+1)
				}
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walkValue(v.Index(i), depth+1)
			}
		}
	}
	walkValue(reflect.ValueOf(node), 0)
}
func literalID(e ast.Expression) string {
	switch n := e.(type) {
	case *ast.NumberLiteral:
		if digits.MatchString(n.Literal) {
			return n.Literal
		}
	case *ast.StringLiteral:
		if digits.MatchString(n.Value.String()) {
			return n.Value.String()
		}
	}
	return ""
}
func unique(values []string) []string {
	set := map[string]bool{}
	for _, v := range values {
		set[v] = true
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
func analyze(body []byte, save func(string, []byte) error) Analysis {
	out := Analysis{Status: "parsed"}
	if len(body) > 4<<20 {
		out.Status = "skipped"
		out.Warnings = append(out.Warnings, "JavaScript exceeds 4 MiB analysis limit")
		return out
	}
	program, err := parser.ParseFile(nil, "bundle.js", body, 0)
	if err != nil {
		out.Status = "unsupported"
		out.Warnings = append(out.Warnings, "JavaScript parser: "+err.Error())
		return out
	}
	source := string(body)
	walk(program, func(n any) {
		if str, ok := n.(*ast.StringLiteral); ok {
			value := str.Value.String()
			if len(out.References) < 512 && len(value) <= 2048 && (strings.HasPrefix(value, "/") || strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://")) {
				out.References = append(out.References, value)
			}
		}
		call, ok := n.(*ast.CallExpression)
		if !ok || len(call.ArgumentList) != 1 {
			return
		}
		dot, ok := call.Callee.(*ast.DotExpression)
		if !ok || dot.Identifier.Name.String() != "push" {
			return
		}
		start, end := int(dot.Left.Idx0())-1, int(dot.Left.Idx1())-1
		if start < 0 || end > len(source) || !strings.Contains(source[start:end], "webpackChunk") {
			return
		}
		arr, ok := call.ArgumentList[0].(*ast.ArrayLiteral)
		if !ok || len(arr.Value) < 2 {
			return
		}
		object, ok := arr.Value[1].(*ast.ObjectLiteral)
		if !ok {
			return
		}
		for _, property := range object.Value {
			prop, ok := property.(*ast.PropertyKeyed)
			if !ok || prop.Computed {
				continue
			}
			id := literalID(prop.Key)
			if id == "" {
				continue
			}
			var params *ast.ParameterList
			switch fn := prop.Value.(type) {
			case *ast.FunctionLiteral:
				params = fn.ParameterList
			case *ast.ArrowFunctionLiteral:
				params = fn.ParameterList
			default:
				continue
			}
			if len(out.Modules) >= 2048 {
				out.Warnings = append(out.Warnings, "Module limit reached")
				break
			}
			m := Module{ID: id, StartByte: int(prop.Value.Idx0()) - 1, EndByte: int(prop.Value.Idx1()) - 1}
			if m.StartByte < 0 || m.EndByte > len(body) || m.StartByte >= m.EndByte {
				continue
			}
			requireName := ""
			if params != nil && len(params.List) >= 3 {
				if ident, ok := params.List[2].Target.(*ast.Identifier); ok {
					requireName = ident.Name.String()
				}
			}
			if requireName != "" {
				walk(prop.Value, func(child any) {
					c, ok := child.(*ast.CallExpression)
					if !ok {
						return
					}
					if ident, ok := c.Callee.(*ast.Identifier); ok && ident.Name.String() == requireName && len(c.ArgumentList) == 1 {
						if id := literalID(c.ArgumentList[0]); id != "" {
							m.Requires = append(m.Requires, id)
						}
					}
					if d, ok := c.Callee.(*ast.DotExpression); ok && d.Identifier.Name.String() == "e" {
						if ident, ok := d.Left.(*ast.Identifier); ok && ident.Name.String() == requireName {
							id := ""
							if len(c.ArgumentList) == 1 {
								id = literalID(c.ArgumentList[0])
							}
							if id == "" {
								m.UnresolvedLazyCalls++
							} else {
								m.LazyChunkIDs = append(m.LazyChunkIDs, id)
							}
						}
					}
				})
			}
			m.Requires = unique(m.Requires)
			m.LazyChunkIDs = unique(m.LazyChunkIDs)
			m.File = fmt.Sprintf("module-%s-%d.js", id, m.StartByte)
			if err := save(m.File, body[m.StartByte:m.EndByte]); err != nil {
				out.Warnings = append(out.Warnings, err.Error())
				m.File = ""
			}
			out.Modules = append(out.Modules, m)
		}
	})
	out.References = unique(out.References)
	if len(out.Modules) == 0 {
		out.Status = "no_supported_modules"
	}
	return out
}
