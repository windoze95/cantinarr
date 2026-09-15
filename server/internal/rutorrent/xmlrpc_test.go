package rutorrent

import (
	"strings"
	"testing"
)

func TestEncodeCallRendersEveryParameterType(t *testing.T) {
	got, err := encodeCall("d.multicall2", "", "main", []string{"d.hash=", "d.name="}, 7, int64(1<<40), true, "a<b&c")
	if err != nil {
		t.Fatalf("encodeCall: %v", err)
	}
	want := `<?xml version="1.0"?><methodCall><methodName>d.multicall2</methodName><params>` +
		`<param><value><string></string></value></param>` +
		`<param><value><string>main</string></value></param>` +
		`<param><value><array><data><value><string>d.hash=</string></value><value><string>d.name=</string></value></data></array></value></param>` +
		`<param><value><i4>7</i4></value></param>` +
		`<param><value><i8>1099511627776</i8></value></param>` +
		`<param><value><boolean>1</boolean></value></param>` +
		`<param><value><string>a&lt;b&amp;c</string></value></param>` +
		`</params></methodCall>`
	if string(got) != want {
		t.Errorf("encodeCall =\n%s\nwant\n%s", got, want)
	}
	if _, err := encodeCall("x", 1.5); err == nil {
		t.Error("encodeCall accepted a float, want an error")
	}
}

func TestDecodeMulticallRowsWithI8AndUntypedValues(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?>
<methodResponse><params><param><value><array><data>
  <value><array><data>
    <value><string>ABCDEF0123</string></value>
    <value>Fedora Workstation</value>
    <value><i8>2147483648000</i8></value>
    <value><i8>0</i8></value>
    <value><i4>1</i4></value>
  </data></array></value>
  <value><array><data>
    <value><string>FEDCBA</string></value>
    <value></value>
    <value><i8>10</i8></value>
    <value><i8>5</i8></value>
    <value><i4>0</i4></value>
  </data></array></value>
</data></array></value></param></params></methodResponse>`
	v, err := decodeResponse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	if v.Kind != "array" || len(v.Array) != 2 {
		t.Fatalf("value = %+v, want an array of 2 rows", v)
	}
	row := v.Array[0]
	if len(row.Array) != 5 || row.Array[0].String() != "ABCDEF0123" || row.Array[1].String() != "Fedora Workstation" ||
		row.Array[2].Int64() != 2147483648000 || row.Array[4].Int64() != 1 {
		t.Errorf("row 0 = %+v", row)
	}
	if v.Array[1].Array[1].Kind != "string" || v.Array[1].Array[1].Str != "" {
		t.Errorf("empty untyped value = %+v, want an empty string", v.Array[1].Array[1])
	}
}

func TestDecodeScalarsAndFault(t *testing.T) {
	v, err := decodeResponse(strings.NewReader(`<methodResponse><params><param><value><string>0.9.8</string></value></param></params></methodResponse>`))
	if err != nil || v.String() != "0.9.8" {
		t.Fatalf("string response = %+v, %v", v, err)
	}
	v, err = decodeResponse(strings.NewReader(`<methodResponse><params><param><value><i8>123456</i8></value></param></params></methodResponse>`))
	if err != nil || v.Int64() != 123456 {
		t.Fatalf("i8 response = %+v, %v", v, err)
	}
	v, err = decodeResponse(strings.NewReader(`<methodResponse><params><param><value><boolean>1</boolean></value></param></params></methodResponse>`))
	if err != nil || !v.Bool || v.Int64() != 1 {
		t.Fatalf("boolean response = %+v, %v", v, err)
	}
	v, err = decodeResponse(strings.NewReader(`<methodResponse><params><param><value><double>12.5</double></value></param></params></methodResponse>`))
	if err != nil || v.Double != 12.5 {
		t.Fatalf("double response = %+v, %v", v, err)
	}

	_, err = decodeResponse(strings.NewReader(`<methodResponse><fault><value><struct>` +
		`<member><name>faultCode</name><value><i4>-501</i4></value></member>` +
		`<member><name>faultString</name><value><string>Could not find info-hash.</string></value></member>` +
		`</struct></value></fault></methodResponse>`))
	fault, ok := err.(*Fault)
	if !ok || fault.Code != -501 || fault.Message != "Could not find info-hash." {
		t.Fatalf("fault = %v (%T), want code -501 with rtorrent's message", err, err)
	}

	if _, err := decodeResponse(strings.NewReader(`<html><body>ruTorrent</body></html>`)); err == nil {
		t.Error("a web page decoded without error")
	}
	if _, err := decodeResponse(strings.NewReader(`<methodResponse><params></params></methodResponse>`)); err == nil {
		t.Error("an empty response decoded without error")
	}
}
