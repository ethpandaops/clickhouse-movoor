package tiering

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

type TableLayout struct {
	Database        string
	Table           string
	PartitionKey    string
	Basis           AgeBasis
	GroupColumns    []string
	AgeExpression   string
	AgeField        string
	FrontierDivisor int64
	TimeFunction    string
	TimeZone        string
	Generation      string
}

type PartitionValue struct {
	Raw        string
	Elements   []string
	GroupKey   string
	AgeString  string
	AgeInteger int64
}

func ParseLayout(database string, table string, partitionKey string, settings TierSettings) (TableLayout, error) {
	expr := strings.TrimSpace(partitionKey)
	if expr == "" || strings.EqualFold(expr, "tuple()") {
		return TableLayout{}, errors.New("empty or tuple() partition key is not tierable")
	}

	elements, err := partitionKeyElements(expr)
	if err != nil {
		return TableLayout{}, err
	}

	layout := TableLayout{
		Database:     database,
		Table:        table,
		PartitionKey: expr,
		Basis:        settings.Age.Basis,
		Generation:   expr + "|" + string(settings.Age.Basis),
	}
	ageElements := 0
	for _, element := range elements {
		kind := classifyLayoutElement(element)
		switch {
		case settings.Age.Basis == AgeBasisFrontier && kind.frontier:
			if kind.field != settings.Age.Field {
				return TableLayout{}, fmt.Errorf("frontier field mismatch: partition key uses %q, config uses %q", kind.field, settings.Age.Field)
			}
			ageElements++
			layout.AgeExpression = element
			layout.AgeField = kind.field
			layout.FrontierDivisor = kind.divisor
		case settings.Age.Basis == AgeBasisPartitionTime && kind.timeFunction != "":
			ageElements++
			layout.AgeExpression = element
			layout.AgeField = kind.field
			layout.TimeFunction = kind.timeFunction
			layout.TimeZone = kind.timeZone
		case kind.bareColumn:
			layout.GroupColumns = append(layout.GroupColumns, element)
		default:
			return TableLayout{}, fmt.Errorf("partition key element %q is not a supported bare group column or age expression", element)
		}
	}
	if ageElements != 1 {
		return TableLayout{}, fmt.Errorf("partition key must contain exactly one %s age expression, found %d", settings.Age.Basis, ageElements)
	}
	return layout, nil
}

func (l TableLayout) ParsePartition(raw string) (PartitionValue, error) {
	value := PartitionValue{Raw: raw}
	elements, err := parsePartitionElements(raw)
	if err != nil {
		return PartitionValue{}, err
	}
	expected := len(l.GroupColumns) + 1
	if len(elements) != expected {
		return PartitionValue{}, fmt.Errorf("partition literal has %d elements, layout expects %d", len(elements), expected)
	}
	value.Elements = elements
	value.AgeString = elements[len(elements)-1]
	if l.Basis == AgeBasisFrontier {
		age, parseErr := strconv.ParseInt(value.AgeString, 10, 64)
		if parseErr != nil {
			return PartitionValue{}, fmt.Errorf("frontier age value %q is not an integer: %w", value.AgeString, parseErr)
		}
		value.AgeInteger = age
	}
	if len(elements) > 1 {
		value.GroupKey = strings.Join(elements[:len(elements)-1], "\x00")
	}
	return value, nil
}

func ValidatePartitionLiteralSyntax(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("partition literal is empty")
	}
	if strings.HasPrefix(strings.TrimSpace(raw), "(") {
		_, err := parseTupleLiteral(strings.TrimSpace(raw))
		return err
	}
	return nil
}

func partitionKeyElements(expr string) ([]string, error) {
	if strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		return splitTopLevel(expr[1 : len(expr)-1])
	}
	return []string{expr}, nil
}

func parsePartitionElements(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "(") {
		return parseTupleLiteral(trimmed)
	}
	return []string{raw}, nil
}

//nolint:gocognit,nestif // This explicit scanner keeps ClickHouse tuple parsing auditable.
func parseTupleLiteral(raw string) ([]string, error) {
	if !strings.HasPrefix(raw, "(") || !strings.HasSuffix(raw, ")") {
		return nil, fmt.Errorf("tuple partition literal %q must start with '(' and end with ')'", raw)
	}
	body := strings.TrimSpace(raw[1 : len(raw)-1])
	if body == "" {
		return nil, errors.New("tuple partition literal must not be empty")
	}
	var elements []string
	for len(body) > 0 {
		body = strings.TrimLeftFunc(body, unicode.IsSpace)
		var value string
		var err error
		if strings.HasPrefix(body, "'") {
			value, body, err = readQuotedTupleString(body)
			if err != nil {
				return nil, err
			}
		} else {
			idx := topLevelComma(body)
			if idx < 0 {
				value = strings.TrimSpace(body)
				body = ""
			} else {
				value = strings.TrimSpace(body[:idx])
				if strings.TrimSpace(body[idx+1:]) == "" {
					return nil, errors.New("tuple partition literal has a trailing comma")
				}
				body = body[idx:]
			}
			if value == "" {
				return nil, errors.New("tuple partition literal contains an empty unquoted element")
			}
		}
		elements = append(elements, value)
		body = strings.TrimLeftFunc(body, unicode.IsSpace)
		if body == "" {
			break
		}
		if !strings.HasPrefix(body, ",") {
			return nil, fmt.Errorf("tuple partition literal has unexpected tail %q", body)
		}
		body = body[1:]
	}
	return elements, nil
}

func readQuotedTupleString(raw string) (string, string, error) {
	var b strings.Builder
	escaped := false
	for i := 1; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			switch ch {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(ch)
			}
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			escaped = true
		case '\'':
			return b.String(), raw[i+1:], nil
		default:
			b.WriteByte(ch)
		}
	}
	return "", "", fmt.Errorf("tuple string %q is unterminated", raw)
}

func topLevelComma(raw string) int {
	depth := 0
	inQuote := false
	escaped := false
	for i, ch := range raw {
		if inQuote {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '\'':
				inQuote = false
			}
			continue
		}
		switch ch {
		case '\'':
			inQuote = true
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitTopLevel(raw string) ([]string, error) {
	var out []string
	start := 0
	depth := 0
	inQuote := false
	escaped := false
	for i, ch := range raw {
		if inQuote {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '\'':
				inQuote = false
			}
			continue
		}
		switch ch {
		case '\'':
			inQuote = true
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("expression %q has unbalanced parentheses", raw)
			}
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(raw[start:i]))
				start = i + 1
			}
		}
	}
	if inQuote || depth != 0 {
		return nil, fmt.Errorf("expression %q has unbalanced quotes or parentheses", raw)
	}
	out = append(out, strings.TrimSpace(raw[start:]))
	if slices.Contains(out, "") {
		return nil, fmt.Errorf("expression %q contains an empty tuple element", raw)
	}
	return out, nil
}

type layoutElementKind struct {
	bareColumn   bool
	frontier     bool
	field        string
	divisor      int64
	timeFunction string
	timeZone     string
}

func classifyLayoutElement(raw string) layoutElementKind {
	element := strings.TrimSpace(raw)
	if isBareIdentifier(element) {
		return layoutElementKind{bareColumn: true, field: element}
	}
	if strings.HasPrefix(element, "intDiv(") && strings.HasSuffix(element, ")") {
		args, err := splitTopLevel(element[len("intDiv(") : len(element)-1])
		if err == nil && len(args) == 2 && isBareIdentifier(args[0]) {
			divisor, parseErr := strconv.ParseInt(strings.TrimSpace(args[1]), 10, 64)
			if parseErr == nil && divisor > 0 {
				return layoutElementKind{frontier: true, field: strings.TrimSpace(args[0]), divisor: divisor}
			}
		}
	}
	if kind := classifyTimeLayoutElement(element); kind.timeFunction != "" {
		return kind
	}
	return layoutElementKind{}
}

func classifyTimeLayoutElement(element string) layoutElementKind {
	for _, fn := range []string{"toYYYYMM", "toYYYYMMDD", "toDate", "toStartOfMonth", "toStartOfWeek", "toStartOfDay"} {
		prefix := fn + "("
		if !strings.HasPrefix(element, prefix) || !strings.HasSuffix(element, ")") {
			continue
		}
		args, err := splitTopLevel(element[len(prefix) : len(element)-1])
		if err != nil || len(args) == 0 || !isBareIdentifier(args[0]) {
			return layoutElementKind{}
		}
		timeZone := ""
		if len(args) >= 2 {
			timeZone = parseTimezoneArgument(args[1])
			if timeZone == "" {
				return layoutElementKind{}
			}
		}
		return layoutElementKind{timeFunction: fn, field: strings.TrimSpace(args[0]), timeZone: timeZone}
	}
	return layoutElementKind{}
}

func parseTimezoneArgument(raw string) string {
	value, tail, err := readQuotedTupleString(strings.TrimSpace(raw))
	if err != nil || strings.TrimSpace(tail) != "" {
		return ""
	}
	return value
}

func isBareIdentifier(raw string) bool {
	if raw == "" {
		return false
	}
	for i, ch := range raw {
		if i == 0 {
			if ch != '_' && !unicode.IsLetter(ch) {
				return false
			}
			continue
		}
		if ch != '_' && !unicode.IsLetter(ch) && !unicode.IsDigit(ch) {
			return false
		}
	}
	return true
}
