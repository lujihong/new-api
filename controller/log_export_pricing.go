package controller

import (
	"encoding/base64"
	"strings"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

type exportPriceTier struct {
	label, condition string
	prices           map[string]float64
	fixed            *float64
	image            bool
}

func exportASTNumber(n ast.Node) (float64, bool) {
	switch v := n.(type) {
	case *ast.IntegerNode:
		return float64(v.Value), true
	case *ast.FloatNode:
		return v.Value, true
	}
	return 0, false
}
func exportASTCall(n ast.Node, name string) (*ast.CallNode, bool) {
	c, ok := n.(*ast.CallNode)
	if !ok {
		return nil, false
	}
	id, ok := c.Callee.(*ast.IdentifierNode)
	return c, ok && id.Value == name
}
func exportASTTerms(n ast.Node) []ast.Node {
	if b, ok := n.(*ast.BinaryNode); ok && b.Operator == "+" {
		return append(exportASTTerms(b.Left), exportASTTerms(b.Right)...)
	}
	return []ast.Node{n}
}
func exportPriceLeaf(n ast.Node) (exportPriceTier, bool) {
	t := exportPriceTier{prices: map[string]float64{}}
	call, ok := exportASTCall(n, "tier")
	if !ok || len(call.Arguments) != 2 {
		return t, false
	}
	label, ok := call.Arguments[0].(*ast.StringNode)
	if !ok {
		return t, false
	}
	t.label = label.Value
	if fixed, ok := exportASTCall(call.Arguments[1], "fixed"); ok && len(fixed.Arguments) == 1 {
		n, ok := exportASTNumber(fixed.Arguments[0])
		if !ok || n < 0 {
			return t, false
		}
		t.fixed = &n
		return t, true
	}
	for _, term := range exportASTTerms(call.Arguments[1]) {
		b, ok := term.(*ast.BinaryNode)
		if !ok || b.Operator != "*" {
			return t, false
		}
		input := b.Left
		price, ok := exportASTNumber(b.Right)
		if !ok {
			input = b.Right
			price, ok = exportASTNumber(b.Left)
		}
		if !ok || price < 0 {
			return t, false
		}
		id, ok := input.(*ast.IdentifierNode)
		if !ok {
			return t, false
		}
		known := false
		for _, v := range exportBillingVariables {
			if id.Value == v.key {
				known = true
				break
			}
		}
		if !known {
			return t, false
		}
		if _, exists := t.prices[id.Value]; exists {
			return t, false
		}
		t.prices[id.Value] = price
	}
	return t, len(t.prices) > 0
}
func exportRuleFactor(n ast.Node) (map[string]any, bool) {
	c, ok := n.(*ast.ConditionalNode)
	if !ok {
		return nil, false
	}
	mul, ok := exportASTNumber(c.Exp1)
	fallback, fok := exportASTNumber(c.Exp2)
	if !ok || !fok || fallback != 1 || mul < 0 {
		return nil, false
	}
	return map[string]any{"cond": c.Cond.String(), "multiplier": mul, "matched": false}, true
}
func exportTokenCondition(n ast.Node) bool {
	b, ok := n.(*ast.BinaryNode)
	if !ok {
		return false
	}
	if b.Operator == "&&" {
		return exportTokenCondition(b.Left) && exportTokenCondition(b.Right)
	}
	if b.Operator != "<" && b.Operator != "<=" && b.Operator != ">" && b.Operator != ">=" {
		return false
	}
	id, ok := b.Left.(*ast.IdentifierNode)
	if !ok || (id.Value != "p" && id.Value != "c" && id.Value != "len") {
		return false
	}
	v, ok := exportASTNumber(b.Right)
	return ok && v >= 0
}

func exportPriceTree(n ast.Node) ([]exportPriceTier, []any, bool) {
	if b, ok := n.(*ast.BinaryNode); ok && b.Operator == "*" {
		for _, pair := range [][2]ast.Node{{b.Left, b.Right}, {b.Right, b.Left}} {
			if id, ok := pair[0].(*ast.IdentifierNode); ok && id.Value == "image_count" {
				tiers, rules, ok := exportPriceTree(pair[1])
				for i := range tiers {
					tiers[i].image = true
				}
				return tiers, rules, ok
			}
			if rule, ok := exportRuleFactor(pair[0]); ok {
				tiers, rules, ok := exportPriceTree(pair[1])
				return tiers, append(rules, rule), ok
			}
		}
	}
	if c, ok := n.(*ast.ConditionalNode); ok {
		if !exportTokenCondition(c.Cond) {
			return nil, nil, false
		}
		left, lr, lok := exportPriceTree(c.Exp1)
		right, rr, rok := exportPriceTree(c.Exp2)
		if !lok || !rok {
			return nil, nil, false
		}
		for i := range left {
			cond := c.Cond.String()
			if left[i].condition != "" {
				cond += " && " + left[i].condition
			}
			left[i].condition = cond
		}
		// Ordered token chains display the fallback without a manufactured condition.
		return append(left, right...), append(lr, rr...), true
	}
	t, ok := exportPriceLeaf(n)
	if !ok {
		return nil, nil, false
	}
	return []exportPriceTier{t}, nil, true
}
func exportNormalizeTier(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), ""))
	return strings.NewReplacer("<=", "<", ">=", ">", "≤", "<", "≥", ">", "＜", "<", "＞", ">").Replace(s)
}
func exportCondition(s string) string {
	// Preserve all operand values and paths; only localize the known identifiers.
	tree, err := parser.Parse(s)
	if err != nil {
		return s
	}
	var describe func(ast.Node) string
	describe = func(n ast.Node) string {
		switch v := n.(type) {
		case *ast.IdentifierNode:
			if label := map[string]string{"p": "输入", "c": "输出", "len": "完整输入长度", "image_count": "图像数量"}[v.Value]; label != "" {
				return label
			}
			return v.Value
		case *ast.BinaryNode:
			op := v.Operator
			if op == "&&" {
				op = "且"
			}
			if op == "||" {
				op = "或"
			}
			return "(" + describe(v.Left) + " " + op + " " + describe(v.Right) + ")"
		case *ast.CallNode:
			if id, ok := v.Callee.(*ast.IdentifierNode); ok {
				label := map[string]string{"param": "请求体参数", "header": "请求头", "hour": "小时", "minute": "分钟", "weekday": "星期", "month": "月份", "day": "日期", "u": "用量参数"}[id.Value]
				if label != "" {
					args := []string{}
					for _, a := range v.Arguments {
						args = append(args, a.String())
					}
					return label + "(" + strings.Join(args, ", ") + ")"
				}
			}
		}
		return n.String()
	}
	return describe(tree.Node)
}

func parsePriceTiers(encoded string) ([]exportPriceTier, bool) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	expr := strings.TrimPrefix(string(decoded), "v1:")
	tree, err := parser.Parse(expr)
	if err != nil {
		return nil, false
	}
	tiers, _, ok := exportPriceTree(tree.Node)
	return tiers, ok
}
