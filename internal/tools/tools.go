// Package tools 提供 mindAgent 的内置工具，供 ReAct 循环调用。
package tools

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"strconv"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// trace 打印工具调用轨迹，方便在 CLI 里观察 ReAct 的 Thought/Action 过程。
func trace(format string, args ...any) {
	log.Printf("🔧 [工具] "+format, args...)
}

// TimeInput 当前时间工具的入参（无参数）。
type TimeInput struct{}

// CurrentTime 返回“获取当前时间”工具，无需参数。
func CurrentTime() (tool.InvokableTool, error) {
	return utils.InferTool("get_current_time", "获取当前的日期、时间和星期几，无需参数。当用户询问现在几点/今天日期时使用",
		func(ctx context.Context, _ TimeInput) (string, error) {
			trace("get_current_time()")
			now := time.Now()
			return now.Format("2006-01-02 15:04:05 ") + weekdayCN(now), nil
		})
}

func weekdayCN(t time.Time) string {
	names := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	return names[t.Weekday()]
}

// CalcInput 计算器工具的入参。
type CalcInput struct {
	// Expression 待计算的四则运算表达式，例如 "(1+2)*3.5"
	Expression string `json:"expression" jsonschema:"description=待计算的四则运算表达式，支持 + - * / 和括号"`
}

// Calculator 返回“计算器”工具，基于 go/parser 解析表达式求值，只允许纯算术，不执行任意代码。
func Calculator() (tool.InvokableTool, error) {
	return utils.InferTool("calculator", "计算四则运算表达式的值，支持加减乘除和括号。当需要精确数值计算时使用，不要心算",
		func(ctx context.Context, in CalcInput) (string, error) {
			trace("calculator(%s)", in.Expression)
			v, err := evalArith(in.Expression)
			if err != nil {
				return "", fmt.Errorf("表达式无法计算: %w", err)
			}
			return strconv.FormatFloat(v, 'f', -1, 64), nil
		})
}

// evalArith 用 go/parser 把表达式解析成 AST，只允许 + - * / 和数字字面量。
func evalArith(expr string) (float64, error) {
	if expr == "" {
		return 0, fmt.Errorf("空表达式")
	}
	node, err := parser.ParseExpr(expr)
	if err != nil {
		return 0, err
	}
	return evalNode(node)
}

func evalNode(n ast.Expr) (float64, error) {
	switch e := n.(type) {
	case *ast.BinaryExpr:
		l, err := evalNode(e.X)
		if err != nil {
			return 0, err
		}
		r, err := evalNode(e.Y)
		if err != nil {
			return 0, err
		}
		switch e.Op {
		case token.ADD:
			return l + r, nil
		case token.SUB:
			return l - r, nil
		case token.MUL:
			return l * r, nil
		case token.QUO:
			if r == 0 {
				return 0, fmt.Errorf("除数为零")
			}
			return l / r, nil
		}
		return 0, fmt.Errorf("不支持的操作符 %v", e.Op)
	case *ast.UnaryExpr:
		v, err := evalNode(e.X)
		if err != nil {
			return 0, err
		}
		switch e.Op {
		case token.SUB:
			return -v, nil
		case token.ADD:
			return v, nil
		}
		return 0, fmt.Errorf("不支持的一元操作符 %v", e.Op)
	case *ast.ParenExpr:
		return evalNode(e.X)
	case *ast.BasicLit:
		if e.Kind == token.FLOAT || e.Kind == token.INT {
			return strconv.ParseFloat(e.Value, 64)
		}
	}
	return 0, fmt.Errorf("不支持的表达式节点 %T", n)
}
