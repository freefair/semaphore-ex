package db

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	coreDB "github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

const (
	workflowConditionProgramVersion = 1
	maxWorkflowConditionLength      = 2048
	maxWorkflowConditionTokens      = 256
)

var workflowConditionFields = map[string]coreDB.WorkflowConditionValueType{
	"result.status":                 coreDB.WorkflowConditionString,
	"result.successful":             coreDB.WorkflowConditionBoolean,
	"result.summary.available":      coreDB.WorkflowConditionBoolean,
	"result.summary.state":          coreDB.WorkflowConditionString,
	"result.summary.expected_hosts": coreDB.WorkflowConditionInteger,
	"result.summary.total_hosts":    coreDB.WorkflowConditionInteger,
	"result.summary.ok_hosts":       coreDB.WorkflowConditionInteger,
	"result.summary.failed_hosts":   coreDB.WorkflowConditionInteger,
}

type workflowConditionTokenKind uint8

const (
	workflowConditionEOF workflowConditionTokenKind = iota
	workflowConditionIdentifier
	workflowConditionStringLiteral
	workflowConditionIntegerLiteral
	workflowConditionBooleanLiteral
	workflowConditionOperator
	workflowConditionLeftParen
	workflowConditionRightParen
)

type workflowConditionToken struct {
	kind  workflowConditionTokenKind
	value string
}

type workflowConditionParser struct {
	tokens []workflowConditionToken
	index  int
}

type workflowConditionOperand struct {
	valueType coreDB.WorkflowConditionValueType
	program   []coreDB.WorkflowConditionInstruction
}

type workflowConditionValue struct {
	valueType coreDB.WorkflowConditionValueType
	boolean   bool
	integer   int64
	text      string
}

// CompileWorkflowCondition parses and type-checks a condition into a bounded,
// data-only stack program. Only workflowConditionFields can be referenced.
func CompileWorkflowCondition(expression string) (coreDB.WorkflowConditionProgram, error) {
	if strings.TrimSpace(expression) == "" {
		return coreDB.WorkflowConditionProgram{}, common_errors.NewValidationError("workflow condition expression is required")
	}
	if len(expression) > maxWorkflowConditionLength {
		return coreDB.WorkflowConditionProgram{}, common_errors.NewValidationError("workflow condition expression is too long")
	}
	tokens, err := lexWorkflowCondition(expression)
	if err != nil {
		return coreDB.WorkflowConditionProgram{}, err
	}
	parser := workflowConditionParser{tokens: tokens}
	instructions, err := parser.parseOr()
	if err != nil {
		return coreDB.WorkflowConditionProgram{}, err
	}
	if parser.current().kind != workflowConditionEOF {
		return coreDB.WorkflowConditionProgram{}, workflowConditionError("unexpected token %q", parser.current().value)
	}
	return coreDB.WorkflowConditionProgram{
		Version: workflowConditionProgramVersion, Instructions: instructions,
	}, nil
}

// EvaluateWorkflowCondition executes only the persisted instruction set over
// the supplied immutable result value.
func EvaluateWorkflowCondition(program coreDB.WorkflowConditionProgram, result coreDB.WorkflowNodeResult) (bool, error) {
	if program.Version != workflowConditionProgramVersion {
		return false, workflowConditionError("unsupported workflow condition program version")
	}
	stack := make([]workflowConditionValue, 0, len(program.Instructions))
	pop := func() (workflowConditionValue, error) {
		if len(stack) == 0 {
			return workflowConditionValue{}, workflowConditionError("workflow condition program stack underflow")
		}
		value := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return value, nil
	}
	for _, instruction := range program.Instructions {
		switch instruction.Operation {
		case "field":
			value, err := workflowConditionFieldValue(instruction.Field, result)
			if err != nil {
				return false, err
			}
			stack = append(stack, value)
		case "literal":
			value, err := workflowConditionLiteralValue(instruction)
			if err != nil {
				return false, err
			}
			stack = append(stack, value)
		case "not":
			value, err := pop()
			if err != nil || value.valueType != coreDB.WorkflowConditionBoolean {
				return false, workflowConditionError("not requires a boolean operand")
			}
			stack = append(stack, workflowConditionValue{valueType: coreDB.WorkflowConditionBoolean, boolean: !value.boolean})
		case "and", "or":
			right, rightErr := pop()
			left, leftErr := pop()
			if rightErr != nil || leftErr != nil || right.valueType != coreDB.WorkflowConditionBoolean || left.valueType != coreDB.WorkflowConditionBoolean {
				return false, workflowConditionError("logical operations require boolean operands")
			}
			value := left.boolean && right.boolean
			if instruction.Operation == "or" {
				value = left.boolean || right.boolean
			}
			stack = append(stack, workflowConditionValue{valueType: coreDB.WorkflowConditionBoolean, boolean: value})
		case "eq", "ne", "lt", "le", "gt", "ge":
			right, rightErr := pop()
			left, leftErr := pop()
			if rightErr != nil || leftErr != nil || right.valueType != left.valueType {
				return false, workflowConditionError("comparison operands have incompatible types")
			}
			matched, err := compareWorkflowConditionValues(instruction.Operation, left, right)
			if err != nil {
				return false, err
			}
			stack = append(stack, workflowConditionValue{valueType: coreDB.WorkflowConditionBoolean, boolean: matched})
		default:
			return false, workflowConditionError("unsupported workflow condition operation %q", instruction.Operation)
		}
	}
	if len(stack) != 1 || stack[0].valueType != coreDB.WorkflowConditionBoolean {
		return false, workflowConditionError("workflow condition program does not produce one boolean")
	}
	return stack[0].boolean, nil
}

func (p *workflowConditionParser) parseOr() ([]coreDB.WorkflowConditionInstruction, error) {
	program, err := p.parseAnd()
	for err == nil && p.accept("||") {
		var right []coreDB.WorkflowConditionInstruction
		right, err = p.parseAnd()
		program = append(program, right...)
		program = append(program, coreDB.WorkflowConditionInstruction{Operation: "or"})
	}
	return program, err
}

func (p *workflowConditionParser) parseAnd() ([]coreDB.WorkflowConditionInstruction, error) {
	program, err := p.parseUnary()
	for err == nil && p.accept("&&") {
		var right []coreDB.WorkflowConditionInstruction
		right, err = p.parseUnary()
		program = append(program, right...)
		program = append(program, coreDB.WorkflowConditionInstruction{Operation: "and"})
	}
	return program, err
}

func (p *workflowConditionParser) parseUnary() ([]coreDB.WorkflowConditionInstruction, error) {
	if p.accept("!") {
		program, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return append(program, coreDB.WorkflowConditionInstruction{Operation: "not"}), nil
	}
	if p.current().kind == workflowConditionLeftParen {
		p.index++
		program, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.current().kind != workflowConditionRightParen {
			return nil, workflowConditionError("closing parenthesis is required")
		}
		p.index++
		return program, nil
	}
	return p.parseComparison()
}

func (p *workflowConditionParser) parseComparison() ([]coreDB.WorkflowConditionInstruction, error) {
	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	token := p.current()
	if token.kind != workflowConditionOperator || !isWorkflowComparison(token.value) {
		if left.valueType != coreDB.WorkflowConditionBoolean {
			return nil, workflowConditionError("condition operands must be compared")
		}
		return left.program, nil
	}
	p.index++
	right, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	if left.valueType != right.valueType {
		return nil, workflowConditionError("comparison operands have incompatible types")
	}
	if left.valueType == coreDB.WorkflowConditionBoolean && token.value != "==" && token.value != "!=" {
		return nil, workflowConditionError("boolean operands support only == and !=")
	}
	operation := map[string]string{"==": "eq", "!=": "ne", "<": "lt", "<=": "le", ">": "gt", ">=": "ge"}[token.value]
	program := append(left.program, right.program...)
	return append(program, coreDB.WorkflowConditionInstruction{Operation: operation}), nil
}

func (p *workflowConditionParser) parseOperand() (workflowConditionOperand, error) {
	token := p.current()
	p.index++
	switch token.kind {
	case workflowConditionIdentifier:
		valueType, ok := workflowConditionFields[token.value]
		if !ok {
			return workflowConditionOperand{}, workflowConditionError("workflow condition field %q is not accessible", token.value)
		}
		return workflowConditionOperand{valueType: valueType, program: []coreDB.WorkflowConditionInstruction{{
			Operation: "field", ValueType: valueType, Field: token.value,
		}}}, nil
	case workflowConditionStringLiteral:
		value := token.value
		return workflowConditionOperand{valueType: coreDB.WorkflowConditionString, program: []coreDB.WorkflowConditionInstruction{{
			Operation: "literal", ValueType: coreDB.WorkflowConditionString, StringValue: &value,
		}}}, nil
	case workflowConditionIntegerLiteral:
		value, err := strconv.ParseInt(token.value, 10, 64)
		if err != nil {
			return workflowConditionOperand{}, workflowConditionError("integer literal is invalid")
		}
		return workflowConditionOperand{valueType: coreDB.WorkflowConditionInteger, program: []coreDB.WorkflowConditionInstruction{{
			Operation: "literal", ValueType: coreDB.WorkflowConditionInteger, IntegerValue: &value,
		}}}, nil
	case workflowConditionBooleanLiteral:
		value := token.value == "true"
		return workflowConditionOperand{valueType: coreDB.WorkflowConditionBoolean, program: []coreDB.WorkflowConditionInstruction{{
			Operation: "literal", ValueType: coreDB.WorkflowConditionBoolean, BooleanValue: &value,
		}}}, nil
	default:
		return workflowConditionOperand{}, workflowConditionError("workflow condition operand is required")
	}
}

func (p *workflowConditionParser) current() workflowConditionToken {
	if p.index >= len(p.tokens) {
		return workflowConditionToken{kind: workflowConditionEOF}
	}
	return p.tokens[p.index]
}

func (p *workflowConditionParser) accept(operator string) bool {
	if p.current().kind != workflowConditionOperator || p.current().value != operator {
		return false
	}
	p.index++
	return true
}

func lexWorkflowCondition(expression string) ([]workflowConditionToken, error) {
	tokens := make([]workflowConditionToken, 0, 16)
	for index := 0; index < len(expression); {
		if unicode.IsSpace(rune(expression[index])) {
			index++
			continue
		}
		if len(tokens) >= maxWorkflowConditionTokens {
			return nil, workflowConditionError("workflow condition contains too many tokens")
		}
		start := index
		switch expression[index] {
		case '(':
			tokens = append(tokens, workflowConditionToken{kind: workflowConditionLeftParen, value: "("})
			index++
		case ')':
			tokens = append(tokens, workflowConditionToken{kind: workflowConditionRightParen, value: ")"})
			index++
		case '"':
			index++
			for index < len(expression) && expression[index] != '"' {
				if expression[index] == '\\' {
					index++
				}
				index++
			}
			if index >= len(expression) {
				return nil, workflowConditionError("string literal is not closed")
			}
			index++
			value, err := strconv.Unquote(expression[start:index])
			if err != nil || len(value) > 256 {
				return nil, workflowConditionError("string literal is invalid")
			}
			tokens = append(tokens, workflowConditionToken{kind: workflowConditionStringLiteral, value: value})
		case '&', '|':
			if index+1 >= len(expression) || expression[index+1] != expression[index] {
				return nil, workflowConditionError("logical operator is invalid")
			}
			tokens = append(tokens, workflowConditionToken{kind: workflowConditionOperator, value: expression[index : index+2]})
			index += 2
		case '=', '!', '<', '>':
			index++
			if index < len(expression) && expression[index] == '=' {
				index++
			}
			operator := expression[start:index]
			if operator == "=" {
				return nil, workflowConditionError("comparison operator is invalid")
			}
			tokens = append(tokens, workflowConditionToken{kind: workflowConditionOperator, value: operator})
		default:
			if expression[index] == '-' || expression[index] >= '0' && expression[index] <= '9' {
				index++
				for index < len(expression) && expression[index] >= '0' && expression[index] <= '9' {
					index++
				}
				tokens = append(tokens, workflowConditionToken{kind: workflowConditionIntegerLiteral, value: expression[start:index]})
				continue
			}
			if !isWorkflowIdentifierStart(expression[index]) {
				return nil, workflowConditionError("unexpected character %q", expression[index])
			}
			index++
			for index < len(expression) && isWorkflowIdentifierPart(expression[index]) {
				index++
			}
			value := expression[start:index]
			kind := workflowConditionIdentifier
			if value == "true" || value == "false" {
				kind = workflowConditionBooleanLiteral
			}
			tokens = append(tokens, workflowConditionToken{kind: kind, value: value})
		}
	}
	return append(tokens, workflowConditionToken{kind: workflowConditionEOF}), nil
}

func isWorkflowIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isWorkflowIdentifierPart(value byte) bool {
	return isWorkflowIdentifierStart(value) || value == '.' || value >= '0' && value <= '9'
}

func isWorkflowComparison(value string) bool {
	switch value {
	case "==", "!=", "<", "<=", ">", ">=":
		return true
	default:
		return false
	}
}

func workflowConditionFieldValue(field string, result coreDB.WorkflowNodeResult) (workflowConditionValue, error) {
	summary := result.Summary
	switch field {
	case "result.status":
		return workflowConditionValue{valueType: coreDB.WorkflowConditionString, text: string(result.Status)}, nil
	case "result.successful":
		return workflowConditionValue{valueType: coreDB.WorkflowConditionBoolean, boolean: result.Successful}, nil
	case "result.summary.available":
		return workflowConditionValue{valueType: coreDB.WorkflowConditionBoolean, boolean: summary != nil}, nil
	case "result.summary.state":
		value := ""
		if summary != nil {
			value = string(summary.State)
		}
		return workflowConditionValue{valueType: coreDB.WorkflowConditionString, text: value}, nil
	case "result.summary.expected_hosts", "result.summary.total_hosts", "result.summary.ok_hosts", "result.summary.failed_hosts":
		value := int64(0)
		if summary != nil {
			switch field {
			case "result.summary.expected_hosts":
				value = int64(summary.ExpectedHosts)
			case "result.summary.total_hosts":
				value = int64(summary.TotalHosts)
			case "result.summary.ok_hosts":
				value = int64(summary.OkHosts)
			case "result.summary.failed_hosts":
				value = int64(summary.FailedHosts)
			}
		}
		return workflowConditionValue{valueType: coreDB.WorkflowConditionInteger, integer: value}, nil
	default:
		return workflowConditionValue{}, workflowConditionError("workflow condition field %q is not accessible", field)
	}
}

func workflowConditionLiteralValue(instruction coreDB.WorkflowConditionInstruction) (workflowConditionValue, error) {
	switch instruction.ValueType {
	case coreDB.WorkflowConditionBoolean:
		if instruction.BooleanValue == nil {
			return workflowConditionValue{}, workflowConditionError("boolean literal is missing")
		}
		return workflowConditionValue{valueType: instruction.ValueType, boolean: *instruction.BooleanValue}, nil
	case coreDB.WorkflowConditionInteger:
		if instruction.IntegerValue == nil {
			return workflowConditionValue{}, workflowConditionError("integer literal is missing")
		}
		return workflowConditionValue{valueType: instruction.ValueType, integer: *instruction.IntegerValue}, nil
	case coreDB.WorkflowConditionString:
		if instruction.StringValue == nil {
			return workflowConditionValue{}, workflowConditionError("string literal is missing")
		}
		return workflowConditionValue{valueType: instruction.ValueType, text: *instruction.StringValue}, nil
	default:
		return workflowConditionValue{}, workflowConditionError("literal type is invalid")
	}
}

func compareWorkflowConditionValues(operation string, left, right workflowConditionValue) (bool, error) {
	compare := 0
	switch left.valueType {
	case coreDB.WorkflowConditionBoolean:
		if left.boolean != right.boolean {
			compare = -1
		}
	case coreDB.WorkflowConditionInteger:
		if left.integer < right.integer {
			compare = -1
		} else if left.integer > right.integer {
			compare = 1
		}
	case coreDB.WorkflowConditionString:
		compare = strings.Compare(left.text, right.text)
	default:
		return false, workflowConditionError("comparison type is invalid")
	}
	switch operation {
	case "eq":
		return compare == 0, nil
	case "ne":
		return compare != 0, nil
	case "lt":
		return compare < 0, nil
	case "le":
		return compare <= 0, nil
	case "gt":
		return compare > 0, nil
	case "ge":
		return compare >= 0, nil
	default:
		return false, workflowConditionError("comparison operation is invalid")
	}
}

func workflowConditionError(format string, args ...any) error {
	return common_errors.NewValidationError(fmt.Sprintf(format, args...))
}
