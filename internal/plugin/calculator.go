package plugin

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// CalculatorPlugin detects arithmetic expressions and evaluates them locally,
// short-circuiting RAG. Supports + - * / % ^ and parentheses on decimals.
type CalculatorPlugin struct {
	base
}

func NewCalculatorPlugin(enabled bool) *CalculatorPlugin {
	p := &CalculatorPlugin{}
	p.SetEnabled(enabled)
	return p
}

func (p *CalculatorPlugin) Name() string        { return "calculator" }
func (p *CalculatorPlugin) Description() string { return "识别并计算数学表达式" }

func (p *CalculatorPlugin) BeforeRAG(ctx context.Context, query string) (*Result, error) {
	expr := extractExpression(query)
	if expr == "" {
		return nil, nil
	}
	val, err := evalExpr(expr)
	if err != nil {
		return nil, nil // not a valid expression -> let RAG handle it
	}
	return &Result{
		Handled: true,
		Answer:  fmt.Sprintf("计算结果：%s = %s", expr, formatNum(val)),
	}, nil
}

func (p *CalculatorPlugin) AfterRAG(ctx context.Context, query, answer string) (*Result, error) {
	return nil, nil
}

// extractExpression pulls a candidate math expression out of the query. It
// strips common Chinese/English lead-ins ("计算", "等于多少", "calculate") and
// requires the remainder to be made only of math characters with at least one
// operator.
func extractExpression(q string) string {
	s := q
	for _, w := range []string{"计算", "请计算", "算一下", "等于多少", "等于几", "是多少", "calculate", "compute", "what is", "="} {
		s = strings.ReplaceAll(s, w, " ")
	}
	s = strings.Map(func(r rune) rune {
		switch r {
		case '＋':
			return '+'
		case '－':
			return '-'
		case '×', '＊':
			return '*'
		case '÷':
			return '/'
		case '（':
			return '('
		case '）':
			return ')'
		}
		return r
	}, s)

	var b strings.Builder
	hasOp, hasDigit := false, false
	for _, r := range s {
		switch {
		case unicode.IsDigit(r) || r == '.':
			b.WriteRune(r)
			hasDigit = true
		case strings.ContainsRune("+-*/%^()", r):
			b.WriteRune(r)
			if r != '(' && r != ')' {
				hasOp = true
			}
		case r == ' ':
			// skip spaces
		default:
			// any other character means this isn't a pure expression
			return ""
		}
	}
	if !hasOp || !hasDigit {
		return ""
	}
	return b.String()
}

func formatNum(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// ---- recursive-descent expression evaluator ----
// grammar: expr = term {(+|-) term}; term = power {(*|/|%) power};
//          power = unary {^ unary}; unary = [-]factor; factor = num | (expr)

type parser struct {
	s   []rune
	pos int
}

func evalExpr(s string) (float64, error) {
	p := &parser{s: []rune(s)}
	v, err := p.expr()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.pos != len(p.s) {
		return 0, fmt.Errorf("unexpected trailing input")
	}
	return v, nil
}

func (p *parser) skipSpace() {
	for p.pos < len(p.s) && p.s[p.pos] == ' ' {
		p.pos++
	}
}

func (p *parser) peek() (rune, bool) {
	p.skipSpace()
	if p.pos < len(p.s) {
		return p.s[p.pos], true
	}
	return 0, false
}

func (p *parser) expr() (float64, error) {
	v, err := p.term()
	if err != nil {
		return 0, err
	}
	for {
		c, ok := p.peek()
		if !ok || (c != '+' && c != '-') {
			return v, nil
		}
		p.pos++
		rhs, err := p.term()
		if err != nil {
			return 0, err
		}
		if c == '+' {
			v += rhs
		} else {
			v -= rhs
		}
	}
}

func (p *parser) term() (float64, error) {
	v, err := p.power()
	if err != nil {
		return 0, err
	}
	for {
		c, ok := p.peek()
		if !ok || (c != '*' && c != '/' && c != '%') {
			return v, nil
		}
		p.pos++
		rhs, err := p.power()
		if err != nil {
			return 0, err
		}
		switch c {
		case '*':
			v *= rhs
		case '/':
			if rhs == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			v /= rhs
		case '%':
			if rhs == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			v = float64(int64(v) % int64(rhs))
		}
	}
}

func (p *parser) power() (float64, error) {
	base, err := p.unary()
	if err != nil {
		return 0, err
	}
	if c, ok := p.peek(); ok && c == '^' {
		p.pos++
		exp, err := p.power() // right-associative
		if err != nil {
			return 0, err
		}
		return ipow(base, exp), nil
	}
	return base, nil
}

func (p *parser) unary() (float64, error) {
	if c, ok := p.peek(); ok && c == '-' {
		p.pos++
		v, err := p.unary()
		return -v, err
	}
	return p.factor()
}

func (p *parser) factor() (float64, error) {
	c, ok := p.peek()
	if !ok {
		return 0, fmt.Errorf("unexpected end of expression")
	}
	if c == '(' {
		p.pos++
		v, err := p.expr()
		if err != nil {
			return 0, err
		}
		if c2, ok := p.peek(); !ok || c2 != ')' {
			return 0, fmt.Errorf("missing closing parenthesis")
		}
		p.pos++
		return v, nil
	}
	// number
	start := p.pos
	for p.pos < len(p.s) && (unicode.IsDigit(p.s[p.pos]) || p.s[p.pos] == '.') {
		p.pos++
	}
	if start == p.pos {
		return 0, fmt.Errorf("expected number at %d", p.pos)
	}
	return strconv.ParseFloat(string(p.s[start:p.pos]), 64)
}

// ipow handles integer and simple float exponents.
func ipow(base, exp float64) float64 {
	result := 1.0
	n := int(exp)
	neg := n < 0
	if neg {
		n = -n
	}
	for i := 0; i < n; i++ {
		result *= base
	}
	if neg {
		return 1 / result
	}
	return result
}
