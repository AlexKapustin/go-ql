// Package walker turns a parsed AST into a SQL fragment suitable for
// gorm.DB.Where, plus the named-argument map that fragment's @placeholders
// reference and a tally of which columns were referenced.
package walker

import (
	"fmt"
	"strings"

	"github.com/AlexKapustin/go-ql/ast"
	"github.com/AlexKapustin/go-ql/dialect"
	"github.com/AlexKapustin/go-ql/schema"
)

// Walker renders a parsed WhereClause to SQL for a specific Dialect and
// schema.
type Walker struct {
	dialect    dialect.Dialect
	components schema.Components
	params     map[string]any
	functions  map[string]dialect.FuncRenderer

	args  *argBuilder
	usage map[string]*schema.FieldUsage
}

// New returns a Walker. customFns are merged over the dialect's built-in
// function table (custom names win on collision; built-in names used by
// the parser's dedicated grammar rules — concat, trim, date_add, etc. —
// cannot be reached by a custom function of the same name, since the
// parser always resolves those names to its own dedicated AST shapes).
func New(d dialect.Dialect, components schema.Components, params map[string]any, customFns map[string]dialect.FuncRenderer) *Walker {
	functions := make(map[string]dialect.FuncRenderer)
	for k, v := range d.Functions() {
		functions[k] = v
	}
	for k, v := range customFns {
		functions[k] = v
	}
	return &Walker{
		dialect:    d,
		components: components,
		params:     params,
		functions:  functions,
		args:       newArgBuilder(),
		usage:      make(map[string]*schema.FieldUsage),
	}
}

// Walk renders wc to a parenthesized SQL fragment the named-argument map its @placeholders
// reference, and per-table field usage counts.
func (w *Walker) Walk(wc *ast.WhereClause) (sql string, args map[string]any, usage map[string]schema.FieldUsage, err error) {
	if wc.Expr == nil {
		return "", w.args.args, w.usageResult(), nil
	}
	inner, err := w.render(wc.Expr)
	if err != nil {
		return "", nil, nil, err
	}
	return "(" + inner + ")", w.args.args, w.usageResult(), nil
}

func (w *Walker) usageResult() map[string]schema.FieldUsage {
	out := make(map[string]schema.FieldUsage, len(w.usage))
	for k, v := range w.usage {
		out[k] = *v
	}
	return out
}

func (w *Walker) render(n ast.Node) (string, error) {
	switch v := n.(type) {

	case *ast.ConditionalExpression:
		parts, err := w.renderAll(v.Terms)
		if err != nil {
			return "", err
		}
		return strings.Join(parts, " OR "), nil

	case *ast.ConditionalTerm:
		parts, err := w.renderAll(v.Factors)
		if err != nil {
			return "", err
		}
		return strings.Join(parts, " AND "), nil

	case *ast.ConditionalFactor:
		s, err := w.render(v.Primary)
		if err != nil {
			return "", err
		}
		if v.Not {
			return "NOT " + s, nil
		}
		return s, nil

	case *ast.ConditionalPrimary:
		if v.Simple != nil {
			return w.render(v.Simple)
		}
		s, err := w.render(v.Paren)
		if err != nil {
			return "", err
		}
		return "(" + s + ")", nil

	case *ast.ComparisonExpression:
		l, err := w.renderAgainst(v.Left, v.Right)
		if err != nil {
			return "", err
		}
		r, err := w.renderAgainst(v.Right, v.Left)
		if err != nil {
			return "", err
		}
		return l + " " + v.Operator + " " + r, nil

	case *ast.BetweenExpression:
		e, err := w.render(v.Expr)
		if err != nil {
			return "", err
		}
		l, err := w.renderAgainst(v.Left, v.Expr)
		if err != nil {
			return "", err
		}
		r, err := w.renderAgainst(v.Right, v.Expr)
		if err != nil {
			return "", err
		}
		s := e
		if v.Not {
			s += " NOT"
		}
		return s + " BETWEEN " + l + " AND " + r, nil

	case *ast.InExpression:
		e, err := w.render(v.Expr)
		if err != nil {
			return "", err
		}
		items := make([]string, len(v.Items))
		for i, item := range v.Items {
			s, err := w.renderAgainst(item, v.Expr)
			if err != nil {
				return "", err
			}
			items[i] = s
		}
		s := e
		if v.Not {
			s += " NOT"
		}
		return s + " IN (" + strings.Join(items, ", ") + ")", nil

	case *ast.LikeExpression:
		l, err := w.render(v.String)
		if err != nil {
			return "", err
		}
		pat, err := w.render(v.Pattern)
		if err != nil {
			return "", err
		}
		s := l
		if v.Not {
			s += " NOT"
		}
		s += " LIKE " + pat
		if v.Escape != nil {
			esc, err := w.render(v.Escape)
			if err != nil {
				return "", err
			}
			s += " ESCAPE " + esc
		}
		return s, nil

	case *ast.NullComparisonExpression:
		if pe, ok := v.Expr.(*ast.PathExpression); ok {
			column, jsonPath, isJSON, err := w.resolvePath(pe)
			if err != nil {
				return "", err
			}
			if isJSON {
				return w.dialect.JSONIsNull(column, jsonPath, v.Not)
			}
			s := column + " IS"
			if v.Not {
				s += " NOT"
			}
			return s + " NULL", nil
		}
		e, err := w.render(v.Expr)
		if err != nil {
			return "", err
		}
		s := e + " IS"
		if v.Not {
			s += " NOT"
		}
		return s + " NULL", nil

	case *ast.PathExpression:
		return w.renderPathExpression(v)

	case *ast.Literal:
		return w.renderLiteral(v)

	case *ast.InputParameter:
		return w.renderInputParameter(v)

	case *ast.ParenthesisExpression:
		s, err := w.render(v.Expr)
		if err != nil {
			return "", err
		}
		return "(" + s + ")", nil

	case *ast.SimpleArithmeticExpression:
		return w.renderChain(v.Terms, v.Ops)

	case *ast.ArithmeticTerm:
		return w.renderChain(v.Factors, v.Ops)

	case *ast.ArithmeticFactor:
		s, err := w.render(v.Primary)
		if err != nil {
			return "", err
		}
		return v.Sign + s, nil

	case *ast.CoalesceExpression:
		parts, err := w.renderAll(v.Exprs)
		if err != nil {
			return "", err
		}
		return "COALESCE(" + strings.Join(parts, ", ") + ")", nil

	case *ast.NullIfExpression:
		f, err := w.render(v.First)
		if err != nil {
			return "", err
		}
		s, err := w.render(v.Second)
		if err != nil {
			return "", err
		}
		return "NULLIF(" + f + ", " + s + ")", nil

	case *ast.GeneralCaseExpression:
		var sb strings.Builder
		sb.WriteString("CASE")
		for _, wc := range v.Whens {
			cond, err := w.render(wc.Cond)
			if err != nil {
				return "", err
			}
			then, err := w.render(wc.Then)
			if err != nil {
				return "", err
			}
			sb.WriteString(" WHEN " + cond + " THEN " + then)
		}
		els, err := w.render(v.Else)
		if err != nil {
			return "", err
		}
		sb.WriteString(" ELSE " + els + " END")
		return sb.String(), nil

	case *ast.SimpleCaseExpression:
		operand, err := w.render(v.Operand)
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		sb.WriteString("CASE " + operand)
		for _, wc := range v.Whens {
			when, err := w.render(wc.When)
			if err != nil {
				return "", err
			}
			then, err := w.render(wc.Then)
			if err != nil {
				return "", err
			}
			sb.WriteString(" WHEN " + when + " THEN " + then)
		}
		els, err := w.render(v.Else)
		if err != nil {
			return "", err
		}
		sb.WriteString(" ELSE " + els + " END")
		return sb.String(), nil

	case *ast.TrimExpression:
		return w.renderTrim(v)

	case *ast.DateAddExpression:
		return w.renderDateAdd(v)

	case *ast.FuncCall:
		return w.renderFuncCall(v)
	}

	return "", fmt.Errorf("ql: walker: unsupported node type %T", n)
}

func (w *Walker) renderAll(nodes []ast.Node) ([]string, error) {
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		s, err := w.render(n)
		if err != nil {
			return nil, err
		}
		parts[i] = s
	}
	return parts, nil
}

// renderChain renders a term/factor chain where ops[i] sits between
// items[i] and items[i+1] (len(ops) == len(items)-1), space-separated.
func (w *Walker) renderChain(items []ast.Node, ops []string) (string, error) {
	parts, err := w.renderAll(items)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(parts[0])
	for i, op := range ops {
		sb.WriteString(" " + op + " " + parts[i+1])
	}
	return sb.String(), nil
}

// renderAgainst renders n, the way it should be written when compared
// against other. If n is a boolean or numeric literal and other is a
// path expression resolving to a JSON-mapped field, it is routed through
// the dialect's JSON-comparable rendering instead of its normal literal
// rendering — see Dialect.JSONComparableBool/JSONComparableNumber for why:
// some engines' JSON-path extraction always returns text, and comparing
// that text against the literal's native SQL form (a bare TRUE/FALSE
// keyword, or an unquoted number) either errors outright or silently
// compares wrong.
func (w *Walker) renderAgainst(n, other ast.Node) (string, error) {
	if lit, ok := n.(*ast.Literal); ok && w.isJSONPath(other) {
		switch lit.Type {
		case ast.LiteralBoolean:
			return w.dialect.JSONComparableBool(lit.Value == "true"), nil
		case ast.LiteralNumeric:
			return w.dialect.JSONComparableNumber(lit.Value), nil
		}
	}
	return w.render(n)
}

// isJSONPath reports whether n is a path expression resolving to a field
// backed by a JSON path rather than a plain column.
func (w *Walker) isJSONPath(n ast.Node) bool {
	pe, ok := n.(*ast.PathExpression)
	if !ok {
		return false
	}
	meta, ok := w.components[pe.Alias]
	if !ok {
		return false
	}
	fm, ok := meta.Fields()[pe.Field]
	return ok && fm.JSONPath != ""
}

func (w *Walker) renderPathExpression(pe *ast.PathExpression) (string, error) {
	column, jsonPath, isJSON, err := w.resolvePath(pe)
	if err != nil {
		return "", err
	}
	if !isJSON {
		return column, nil
	}
	return w.dialect.JSONExtract(column, jsonPath)
}

// resolvePath resolves pe to its quoted column expression and, if it maps
// to a JSON-backed field, the JSON path segments within that column
// (isJSON reports which). It also tallies field usage as a side effect, so
// every caller that resolves a PathExpression — whether via
// renderPathExpression or a dialect-specific case that needs the raw
// column/path instead of an already-extracted expression, such as
// NullComparisonExpression's JSON handling below — must call this exactly
// once per occurrence rather than resolving the field itself.
func (w *Walker) resolvePath(pe *ast.PathExpression) (column string, jsonPath []string, isJSON bool, err error) {
	meta, ok := w.components[pe.Alias]
	if !ok {
		return "", nil, false, fmt.Errorf("ql: alias %q is not defined", pe.Alias)
	}
	fm, ok := meta.Fields()[pe.Field]
	if !ok {
		return "", nil, false, fmt.Errorf("ql: field %q is not defined on %q", pe.Field, pe.Alias)
	}

	table := meta.TableName()
	// Usage is tallied by the resolved database column (fm.Column), not the
	// logical field name — two logical fields backed by the same JSON column
	// (e.g. two different json_path fields both stored in a "metadata" column) count
	// against that one column.
	w.trackUsage(table, meta, fm.Column)

	column = w.dialect.QuoteQualified(table, fm.Column)
	if fm.JSONPath == "" {
		return column, nil, false, nil
	}
	return column, strings.Split(fm.JSONPath, "."), true, nil
}

func (w *Walker) trackUsage(table string, meta schema.TableMetadata, column string) {
	u, ok := w.usage[table]
	if !ok {
		u = &schema.FieldUsage{Table: meta, Fields: map[string]int{}}
		w.usage[table] = u
	}
	u.Fields[column]++
}

func (w *Walker) renderLiteral(lit *ast.Literal) (string, error) {
	switch lit.Type {
	case ast.LiteralString, ast.LiteralDate:
		// Routed through a synthesized placeholder rather than inlined,
		// since the source text could contain arbitrary characters.
		return w.args.Literal(lit.Value), nil
	case ast.LiteralBoolean:
		if lit.Value == "true" {
			return "TRUE", nil
		}
		return "FALSE", nil
	case ast.LiteralNumeric:
		// The lexer only ever produces well-formed digit/./e/+/- text for
		// a numeric literal, so inlining it directly is safe.
		return lit.Value, nil
	}
	return "", fmt.Errorf("ql: walker: invalid literal type %v", lit.Type)
}

func (w *Walker) renderInputParameter(ip *ast.InputParameter) (string, error) {
	v, ok := w.params[ip.Name]
	if !ok {
		return "", fmt.Errorf("ql: input parameter %q is not defined", ip.Name)
	}
	if ip.IsNamed {
		return w.args.Named(ip.Name, v), nil
	}
	return w.args.Positional(ip.Name, v), nil
}

func (w *Walker) renderTrim(t *ast.TrimExpression) (string, error) {
	target, err := w.render(t.Target)
	if err != nil {
		return "", err
	}

	var charSQL string
	if t.Char != nil {
		charSQL, err = w.render(t.Char)
		if err != nil {
			return "", err
		}
	}

	return w.dialect.Trim(t.Mode, charSQL, target)
}

func (w *Walker) renderDateAdd(d *ast.DateAddExpression) (string, error) {
	dateSQL, err := w.render(d.Date)
	if err != nil {
		return "", err
	}
	intervalSQL, err := w.render(d.Interval)
	if err != nil {
		return "", err
	}
	if d.Sub {
		return w.dialect.DateSub(dateSQL, intervalSQL, d.Unit)
	}
	return w.dialect.DateAdd(dateSQL, intervalSQL, d.Unit)
}

func (w *Walker) renderFuncCall(f *ast.FuncCall) (string, error) {
	args, err := w.renderAll(f.Args)
	if err != nil {
		return "", err
	}
	renderer, ok := w.functions[f.Name]
	if !ok {
		return "", fmt.Errorf("ql: unknown function %q", f.Name)
	}
	return renderer(args)
}
