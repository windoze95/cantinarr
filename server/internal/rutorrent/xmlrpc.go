package rutorrent

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A minimal XML-RPC codec for the subset rtorrent speaks: string, i4/i8/int,
// boolean, double, and nested arrays; structs only appear in faults. rtorrent
// answers 64-bit values as <i8>, which the common Go XML-RPC packages do not
// decode, so the client carries its own.

// Fault is an XML-RPC fault answered by rtorrent (or by ruTorrent's proxy
// when it refuses a call).
type Fault struct {
	Code    int
	Message string
}

func (f *Fault) Error() string {
	return fmt.Sprintf("rtorrent fault %d: %s", f.Code, f.Message)
}

// encodeCall renders a methodCall. Supported parameter types: string, int,
// int64, bool, and []string (an array of strings, used for multicall
// command lists).
func encodeCall(method string, params ...interface{}) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0"?><methodCall><methodName>`)
	xml.EscapeText(&b, []byte(method))
	b.WriteString(`</methodName><params>`)
	for _, p := range params {
		b.WriteString(`<param>`)
		if err := encodeValue(&b, p); err != nil {
			return nil, err
		}
		b.WriteString(`</param>`)
	}
	b.WriteString(`</params></methodCall>`)
	return b.Bytes(), nil
}

func encodeValue(b *bytes.Buffer, v interface{}) error {
	b.WriteString(`<value>`)
	switch x := v.(type) {
	case string:
		b.WriteString(`<string>`)
		xml.EscapeText(b, []byte(x))
		b.WriteString(`</string>`)
	case int:
		fmt.Fprintf(b, `<i4>%d</i4>`, x)
	case int64:
		fmt.Fprintf(b, `<i8>%d</i8>`, x)
	case bool:
		if x {
			b.WriteString(`<boolean>1</boolean>`)
		} else {
			b.WriteString(`<boolean>0</boolean>`)
		}
	case []string:
		b.WriteString(`<array><data>`)
		for _, s := range x {
			if err := encodeValue(b, s); err != nil {
				return err
			}
		}
		b.WriteString(`</data></array>`)
	default:
		return fmt.Errorf("xmlrpc: unsupported parameter type %T", v)
	}
	b.WriteString(`</value>`)
	return nil
}

// Value is one decoded XML-RPC value: exactly one of the fields is set, and
// Kind names it ("string", "int", "bool", "double", "array", "struct").
type Value struct {
	Kind   string
	Str    string
	Int    int64
	Bool   bool
	Double float64
	Array  []Value
	Struct map[string]Value
}

// String returns the value as text: strings verbatim, numbers formatted.
func (v Value) String() string {
	switch v.Kind {
	case "string":
		return v.Str
	case "int":
		return strconv.FormatInt(v.Int, 10)
	case "bool":
		if v.Bool {
			return "1"
		}
		return "0"
	case "double":
		return strconv.FormatFloat(v.Double, 'f', -1, 64)
	}
	return ""
}

// Int64 returns the value as an integer: ints as-is, bools as 0/1, numeric
// strings parsed, anything else 0.
func (v Value) Int64() int64 {
	switch v.Kind {
	case "int":
		return v.Int
	case "bool":
		if v.Bool {
			return 1
		}
		return 0
	case "double":
		return int64(v.Double)
	case "string":
		n, _ := strconv.ParseInt(strings.TrimSpace(v.Str), 10, 64)
		return n
	}
	return 0
}

// decodeResponse parses a methodResponse into its single value, or returns
// the fault it carries.
func decodeResponse(r io.Reader) (Value, error) {
	dec := xml.NewDecoder(r)
	var (
		inFault bool
		result  Value
		found   bool
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Value{}, fmt.Errorf("xmlrpc: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "fault":
			inFault = true
		case "value":
			v, err := decodeValue(dec)
			if err != nil {
				return Value{}, err
			}
			if inFault {
				return Value{}, faultFromValue(v)
			}
			if found {
				return Value{}, fmt.Errorf("xmlrpc: more than one value in the response")
			}
			result, found = v, true
		}
	}
	if !found {
		return Value{}, fmt.Errorf("xmlrpc: no value in the response")
	}
	return result, nil
}

func faultFromValue(v Value) error {
	f := &Fault{}
	if v.Kind == "struct" {
		f.Code = int(v.Struct["faultCode"].Int64())
		f.Message = v.Struct["faultString"].String()
	}
	if f.Message == "" {
		f.Message = "unknown fault"
	}
	return f
}

// decodeValue reads the content of a <value> element up to its end tag.
// An untyped <value>text</value> is a string.
func decodeValue(dec *xml.Decoder) (Value, error) {
	var (
		text  strings.Builder
		typed *Value
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			return Value{}, fmt.Errorf("xmlrpc: %w", err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			text.Write(t)
		case xml.StartElement:
			v, err := decodeTyped(dec, t.Name.Local)
			if err != nil {
				return Value{}, err
			}
			typed = &v
		case xml.EndElement:
			if t.Name.Local != "value" {
				return Value{}, fmt.Errorf("xmlrpc: unexpected </%s>", t.Name.Local)
			}
			if typed != nil {
				return *typed, nil
			}
			return Value{Kind: "string", Str: text.String()}, nil
		}
	}
}

// decodeTyped reads a typed element (<string>, <i8>, <array>, …) that has
// just been opened, through its end tag.
func decodeTyped(dec *xml.Decoder, name string) (Value, error) {
	switch name {
	case "array":
		return decodeArray(dec)
	case "struct":
		return decodeStruct(dec)
	}
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return Value{}, fmt.Errorf("xmlrpc: %w", err)
		}
		switch t := tok.(type) {
		case xml.CharData:
			text.Write(t)
		case xml.StartElement:
			return Value{}, fmt.Errorf("xmlrpc: unexpected <%s> inside <%s>", t.Name.Local, name)
		case xml.EndElement:
			return scalar(name, strings.TrimSpace(text.String()))
		}
	}
}

func scalar(name, text string) (Value, error) {
	switch name {
	case "string":
		return Value{Kind: "string", Str: text}, nil
	case "i4", "i8", "int":
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("xmlrpc: bad %s %q", name, text)
		}
		return Value{Kind: "int", Int: n}, nil
	case "boolean":
		return Value{Kind: "bool", Bool: text == "1" || strings.EqualFold(text, "true")}, nil
	case "double":
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return Value{}, fmt.Errorf("xmlrpc: bad double %q", text)
		}
		return Value{Kind: "double", Double: f}, nil
	case "base64", "dateTime.iso8601":
		return Value{Kind: "string", Str: text}, nil
	}
	return Value{}, fmt.Errorf("xmlrpc: unsupported type <%s>", name)
}

// decodeArray reads <array><data><value>…</value>…</data></array> after
// <array> was opened.
func decodeArray(dec *xml.Decoder) (Value, error) {
	out := Value{Kind: "array", Array: []Value{}}
	for {
		tok, err := dec.Token()
		if err != nil {
			return Value{}, fmt.Errorf("xmlrpc: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "data":
				// container only
			case "value":
				v, err := decodeValue(dec)
				if err != nil {
					return Value{}, err
				}
				out.Array = append(out.Array, v)
			default:
				return Value{}, fmt.Errorf("xmlrpc: unexpected <%s> in array", t.Name.Local)
			}
		case xml.EndElement:
			if t.Name.Local == "array" {
				return out, nil
			}
		}
	}
}

// decodeStruct reads <struct><member><name>…</name><value>…</value></member>
// …</struct> after <struct> was opened.
func decodeStruct(dec *xml.Decoder) (Value, error) {
	out := Value{Kind: "struct", Struct: map[string]Value{}}
	var name string
	for {
		tok, err := dec.Token()
		if err != nil {
			return Value{}, fmt.Errorf("xmlrpc: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "member":
				name = ""
			case "name":
				var n string
				if err := dec.DecodeElement(&n, &t); err != nil {
					return Value{}, fmt.Errorf("xmlrpc: %w", err)
				}
				name = n
			case "value":
				v, err := decodeValue(dec)
				if err != nil {
					return Value{}, err
				}
				out.Struct[name] = v
			}
		case xml.EndElement:
			if t.Name.Local == "struct" {
				return out, nil
			}
		}
	}
}
